package supremacy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Alliance — клан игрока на сайте игры. Это не наш справочник кланов:
// альянс заведён в самой Supremacy, игроки состоят в нём по-настоящему,
// и меняется он без нашего ведома.
type Alliance struct {
	ID   string
	Name string
	Tag  string
}

// UserAlliance спрашивает у сайта, в каком альянсе состоит игрок. Работает
// по тому же номеру, который партия отдаёт как siteUserID, поэтому состав
// партии сводится с альянсами без всякой базы.
//
// Заходом в партию этот вызов не считается: он идёт на сайт, а не на игровой
// сервер. Ответ nil без ошибки означает, что игрок ни в каком альянсе
// не состоит.
func (c *Client) UserAlliance(ctx context.Context, siteUserID string) (*Alliance, error) {
	d, err := c.UserDetails(ctx, siteUserID)
	if err != nil {
		return nil, err
	}
	return d.Alliance, nil
}

// UserDetails — то, что сайт рассказывает про игрока одним запросом:
// альянс и нынешний ник. Ник пуст, если сайт его не назвал.
type UserDetails struct {
	Alliance *Alliance
	Username string
}

// UserDetails спрашивает у сайта альянс и ник игрока разом: флаги
// у getUserDetailsFirefly складываются, и второй вопрос лишнего запроса
// не стоит. Ник нужен карточкам: его меняют, а карточка заводится один раз.
func (c *Client) UserDetails(ctx context.Context, siteUserID string) (UserDetails, error) {
	raw, err := c.call(ctx, "getUserDetailsFirefly", []param{
		{"userID", siteUserID},
		{"alliance", "1"},
		{"username", "1"},
	})
	if err != nil {
		return UserDetails{}, err
	}

	alliance, err := parseAlliance(raw)
	if err != nil {
		return UserDetails{}, fmt.Errorf("альянс игрока %s: %w", siteUserID, err)
	}
	name, err := parseUsername(raw)
	if err != nil {
		return UserDetails{}, fmt.Errorf("ник игрока %s: %w", siteUserID, err)
	}
	return UserDetails{Alliance: alliance, Username: name}, nil
}

// Username спрашивает у сайта ник игрока по его номеру. Нужен регистрации:
// человек называет свой игровой ID, а ник в админке берётся из игры, чтобы
// его нельзя было выдумать.
//
// Пустая строка без ошибки означает, что такого игрока сайт не знает.
func (c *Client) Username(ctx context.Context, siteUserID string) (string, error) {
	raw, err := c.call(ctx, "getUserDetailsFirefly", []param{
		{"userID", siteUserID},
		{"username", "1"},
	})
	if err != nil {
		return "", err
	}

	name, err := parseUsername(raw)
	if err != nil {
		return "", fmt.Errorf("ник игрока %s: %w", siteUserID, err)
	}
	return name, nil
}

// parseUsername разбирает ответ про игрока. Про несуществующий номер сайт
// отвечает пустым пользователем, без поля username, — это не ошибка.
func parseUsername(raw json.RawMessage) (string, error) {
	var res struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("разбор: %w", err)
	}
	return strings.TrimSpace(res.Username), nil
}

// parseAlliance разбирает ответ про игрока. Альянс приходит вложенным
// в properties, а у игрока без альянса поле просто null — это не ошибка,
// а полноценный ответ.
func parseAlliance(raw json.RawMessage) (*Alliance, error) {
	var res struct {
		Alliance *struct {
			Properties struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
				Tag  string `json:"tag"`
			} `json:"properties"`
		} `json:"alliance"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор: %w", err)
	}
	if res.Alliance == nil || res.Alliance.Properties.UID == "" {
		return nil, nil
	}
	p := res.Alliance.Properties
	return &Alliance{ID: p.UID, Name: p.Name, Tag: p.Tag}, nil
}

// AllianceMember — участник клана, как его отдаёт сайт: номер на сайте
// игры и ник. Больше про него из состава ничего не нужно — карту красит
// принадлежность к клану, а не заслуги.
type AllianceMember struct {
	SiteUserID string
	Name       string
}

// AllianceRoster забирает клан целиком: сам клан и весь его состав одним
// запросом. Это главный способ узнавать кланы: спрашивать про каждого
// игрока отдельно вышло бы во столько запросов, сколько в клане людей,
// а их до сорока.
func (c *Client) AllianceRoster(ctx context.Context, allianceID string) (*Alliance, []AllianceMember, error) {
	raw, err := c.call(ctx, "getAlliance", []param{
		{"allianceID", allianceID},
		{"members", "1"},
	})
	if err != nil {
		return nil, nil, err
	}
	return parseRoster(raw, allianceID)
}

// parseRoster разбирает ответ про клан. Состав приходит списком рядом
// с самим кланом, у каждого участника нас интересуют только номер и ник.
func parseRoster(raw json.RawMessage, allianceID string) (*Alliance, []AllianceMember, error) {
	var res struct {
		Properties struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
			Tag  string `json:"tag"`
		} `json:"properties"`
		Members []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("разбор клана %s: %w", allianceID, err)
	}
	if res.Properties.UID == "" {
		return nil, nil, fmt.Errorf("клан %s: сайт ответил без клана", allianceID)
	}

	alliance := &Alliance{
		ID: res.Properties.UID, Name: res.Properties.Name, Tag: res.Properties.Tag,
	}
	members := make([]AllianceMember, 0, len(res.Members))
	for _, m := range res.Members {
		if m.ID <= 0 {
			continue
		}
		members = append(members, AllianceMember{
			SiteUserID: strconv.FormatInt(m.ID, 10), Name: m.Username,
		})
	}
	return alliance, members, nil
}

// RankedAlliance — строка рейтинга кланов: сам клан и его место. Из всей
// статистики берём только очки: место и очки объясняют друг друга, а победы,
// поражения и средний счёт на карте не нужны ни в каком виде.
type RankedAlliance struct {
	Alliance
	Rank int
	Elo  int
}

// AllianceRanking забирает страницу рейтинга кланов. Нумерация страниц
// с нуля, на странице numEntries записей — так их считает сам клиент игры,
// откуда имя вызова и параметры и взяты.
//
// Заходом в партию это не считается: вопрос идёт на сайт. Рейтинг общий
// для всех доменов игры, поэтому спрашивать его можно откуда угодно.
func (c *Client) AllianceRanking(ctx context.Context, page, numEntries int) ([]RankedAlliance, error) {
	raw, err := c.call(ctx, "getAllianceRanking", []param{
		{"page", strconv.Itoa(page)},
		{"numEntries", strconv.Itoa(numEntries)},
	})
	if err != nil {
		return nil, err
	}
	return parseRanking(raw)
}

// parseRanking разбирает ответ рейтинга. Клан лежит там же, где и в других
// ответах про кланы, — в properties; место и очки едут рядом, в stats.
//
// Пустой ответ — не ошибка: так рейтинг сообщает, что страница за концом
// списка. Общего числа кланов он не отдаёт вовсе, и глубину списка узнать
// можно только листанием до пустой страницы.
func parseRanking(raw json.RawMessage) ([]RankedAlliance, error) {
	var res struct {
		Entries []struct {
			Properties struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
				Tag  string `json:"tag"`
			} `json:"properties"`
			Stats struct {
				Elo        int `json:"elo"`
				GlobalRank int `json:"globalRank"`
			} `json:"stats"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор рейтинга кланов: %w", err)
	}

	out := make([]RankedAlliance, 0, len(res.Entries))
	for _, e := range res.Entries {
		p := e.Properties
		if p.UID == "" {
			continue
		}
		out = append(out, RankedAlliance{
			Alliance: Alliance{ID: p.UID, Name: p.Name, Tag: p.Tag},
			Rank:     e.Stats.GlobalRank,
			Elo:      e.Stats.Elo,
		})
	}
	return out, nil
}

// allianceWorkers — сколько альянсов спрашиваем одновременно. Игроков
// в партии до сорока, а сайт отвечает не мгновенно: последовательный обход
// не уложился бы в терпение человека, открывшего страницу. Больше десятка
// параллельных запросов слать не хочется — это всё-таки чужой сайт.
const allianceWorkers = 8

// UserAlliances спрашивает альянсы и ники сразу у пачки игроков. Ключ
// в ответе есть у каждого, кого удалось спросить; nil в Alliance означает
// «спросили, альянса нет». Ошибка возвращается первая из случившихся, но ответ при
// этом не пустой: одного упавшего запроса мало, чтобы отказать всей карте.
func (c *Client) UserAlliances(ctx context.Context, siteUserIDs []string) (map[string]UserDetails, error) {
	out := make(map[string]UserDetails, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return out, nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	ids := make(chan string)

	workers := min(allianceWorkers, len(siteUserIDs))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				details, err := c.UserDetails(ctx, id)
				mu.Lock()
				if err != nil {
					if firstEr == nil {
						firstEr = err
					}
				} else {
					out[id] = details
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range siteUserIDs {
		ids <- id
	}
	close(ids)
	wg.Wait()

	return out, firstEr
}

// UserStats — боевой счёт игрока с сайта игры. Приходит тем же вызовом,
// что и клан, только другими полями, и так же не требует захода в партию.
//
// Defeated и Casualties считаются по боям с живыми игроками: сайт держит
// бои с компьютерными отдельным набором, и складывать их не стоит.
type UserStats struct {
	SiteUserID    string
	Level         int
	Defeated      int64
	Casualties    int64
	Games         int
	SoloWins      int
	CoalitionWins int
	OverallScore  int64
}

// UserStats спрашивает у сайта боевой счёт игрока. Работает по тому же
// номеру, что партия отдаёт как siteUserID, и по любому игроку, не только
// по нашему: сам клиент игры так же смотрит чужие профили.
//
// nil без ошибки означает, что сайт про такого ничего не рассказал:
// так отвечает про удалённые аккаунты.
func (c *Client) UserStats(ctx context.Context, siteUserID string) (*UserStats, error) {
	raw, err := c.call(ctx, "getUserDetailsFirefly", []param{
		{"userID", siteUserID},
		{"rankProgress", "1"},
		{"gameStats", "1"},
	})
	if err != nil {
		return nil, err
	}

	stats, err := parseUserStats(raw)
	if err != nil {
		return nil, fmt.Errorf("счёт игрока %s: %w", siteUserID, err)
	}
	if stats != nil {
		stats.SiteUserID = siteUserID
	}
	return stats, nil
}

// parseUserStats разбирает ответ про игрока. Бои лежат по типам юнитов —
// столько строк, сколько в игре видов войск, — и нам нужна только их сумма:
// «кд» считается по всей армии сразу, как и в самом клиенте игры.
func parseUserStats(raw json.RawMessage) (*UserStats, error) {
	var res struct {
		RankProgress *struct {
			Level        int   `json:"currentRankLevel"`
			OverallScore int64 `json:"overallScore"`
		} `json:"rankProgress"`
		GameStats *struct {
			// combatScores — бои с живыми; combatScoresAI лежит рядом
			// и намеренно не читается.
			CombatScores map[string]struct {
				Defeated int64 `json:"defeated"`
				Casualty int64 `json:"casualty"`
			} `json:"combatScores"`
			Totals struct {
				Games         int `json:"gameJoin"`
				SoloWins      int `json:"soloVictory"`
				CoalitionWins int `json:"coalitionVictory"`
			} `json:"gameStatsScore"`
		} `json:"gameStats"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор: %w", err)
	}
	// Ни уровня, ни боёв — про такого сайт не рассказал ничего, и нули
	// записывать нечестно.
	if res.RankProgress == nil && res.GameStats == nil {
		return nil, nil
	}

	var out UserStats
	if r := res.RankProgress; r != nil {
		out.Level, out.OverallScore = r.Level, r.OverallScore
	}
	if g := res.GameStats; g != nil {
		for _, u := range g.CombatScores {
			out.Defeated += u.Defeated
			out.Casualties += u.Casualty
		}
		out.Games, out.SoloWins, out.CoalitionWins = g.Totals.Games, g.Totals.SoloWins, g.Totals.CoalitionWins
	}
	return &out, nil
}

// UserStatsBatch спрашивает счёт сразу у пачки игроков — тем же способом,
// что и кланы: по игроку за раз, но в несколько потоков. Ключ есть у
// каждого, кого удалось спросить; nil в значении означает «спросили,
// а сайт про него молчит».
func (c *Client) UserStatsBatch(ctx context.Context, siteUserIDs []string) (map[string]*UserStats, error) {
	out := make(map[string]*UserStats, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return out, nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	ids := make(chan string)

	workers := min(allianceWorkers, len(siteUserIDs))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				stats, err := c.UserStats(ctx, id)
				mu.Lock()
				if err != nil {
					if firstEr == nil {
						firstEr = err
					}
				} else {
					out[id] = stats
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range siteUserIDs {
		ids <- id
	}
	close(ids)
	wg.Wait()

	return out, firstEr
}
