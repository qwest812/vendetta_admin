package supremacy

// Состав партии живёт не в web-API лобби, а на игровом сервере (gs) — том
// самом, с которым разговаривает клиент игры в браузере. Путь до него такой:
//
//	getGameToken            → адрес gs и токен на вход в партию
//	UltActivateGameAction   → наш playerID в этой партии
//	UltUpdateGameStateAction → полное состояние: игроки, карта, день
//
// Подписи, как в index.php, здесь нет: gs верит токену из getGameToken.
//
// Входом в партию игра считает ровно средний шаг — активацию. Без него
// состояние приходит глазами наблюдателя: то же самое, но своего игрока
// в нём нет, а партия не запоминает, что мы заходили. Отсюда два пути:
//
//	GameState   — игроком: нужен свой playerID, но заход засчитывается,
//	              поэтому дёргать его по таймеру не стоит.
//	ObserveGame — наблюдателем: годится для любой партии, в том числе
//	              чужой, и следа в ней не оставляет.
//
// Токен gs сайт выдаёт и по чужой партии — участие он не проверяет.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// Версия и клиент, которыми представляется браузерный клиент игры.
	// Версия здесь — только начальная догадка: игру обновляют без нас, и на
	// устаревшую она отвечает отказом, из которого видно нужный номер.
	gsVersionDefault = "316"
	gsClient         = "s1914-client-mobile"
)

// reDeployedVersion достаёт актуальный номер клиента из жалобы игры:
// «User client/version for s1914-client-mobile: #316, Deployed client
// version s1914-client-mobile_live: #317».
var reDeployedVersion = regexp.MustCompile(`Deployed client version [^:]*: #(\d+)`)

// gsException — игровой сервер вернул не ответ, а своё исключение.
type gsException struct {
	class  string
	detail string
}

func (e *gsException) Error() string {
	name := strings.TrimPrefix(e.class, "ultshared.")
	if e.detail == "" {
		return name
	}
	return name + ": " + e.detail
}

// version — номер клиента, которого игре не хватило, если отказ именно об этом.
func (e *gsException) version() string {
	if !strings.Contains(e.class, "ClientVersionMismatch") {
		return ""
	}
	if m := reDeployedVersion.FindStringSubmatch(e.detail); m != nil {
		return m[1]
	}
	return ""
}

// Province — провинция на карте партии. Море и озёра сюда не попадают.
// Owner = 0 означает ничейную землю: игрока с таким номером не бывает.
type Province struct {
	ID      int
	Name    string
	Owner   int
	Morale  float64
	Capital bool
	// Built — что в провинции уже построено, номерами из справочника
	// зданий. Уровень здания игра держит отдельно, и нам он не нужен:
	// повторная постройка того же здания и есть следующий уровень.
	Built []int
	// Building — стройки, идущие сейчас. Обычно не больше одной: второй
	// слот даёт премиум, и тогда их бывает две.
	Building []Construction
	// Slots — сколько строек в провинции можно вести разом. Ноль означает
	// «игра не сказала»; считаем такую провинцию односложной.
	Slots int
}

// Free — свободен ли слот под стройку.
func (p Province) Free() bool {
	slots := p.Slots
	if slots < 1 {
		slots = 1
	}
	return len(p.Building) < slots
}

// BuildsUntil — когда закончится самая ранняя из идущих строек. Нулевое
// время означает, что в провинции не строят ничего.
func (p Province) BuildsUntil() time.Time {
	var first time.Time
	for _, c := range p.Building {
		if first.IsZero() || c.Ends.Before(first) {
			first = c.Ends
		}
	}
	return first
}

// Construction — стройка, идущая в провинции: что строят и когда кончат.
// Время окончания называет сама игра, считать его самим не нужно.
type Construction struct {
	UpgradeID int
	Ends      time.Time
}

// Upgrade — здание из справочника партии. Справочник приходит вместе
// с состоянием, поэтому названия в нём уже на языке аккаунта, а цены —
// те, что действуют в этой партии.
// Уровни здания игра держит отдельными записями: «Крепость» второго уровня
// — своё здание, заменяющее собой первое. Replaces говорит, какое именно,
// а Tier считается по этой цепочке: у первого уровня он единица. Без него
// в списке стояло бы пять одинаковых «Крепостей».
type Upgrade struct {
	ID       int
	Name     string
	Build    time.Duration   // сколько строится по справочнику, настоящими часами
	Cost     map[int]float64 // ресурс → сколько стоит
	Replaces int
	Tier     int
}

// Resource — запас ресурса и скорость его прихода. Игра отдаёт запас
// на момент Measured и прирост в секунду — уже за вычетом расхода,
// поэтому он бывает и отрицательным.
type Resource struct {
	ID       int
	Name     string
	Amount   float64
	Measured time.Time
	Rate     float64
}

// AmountAt — сколько ресурса будет к этому времени.
func (r Resource) AmountAt(t time.Time) float64 {
	return r.Amount + r.Rate*t.Sub(r.Measured).Seconds()
}

// Enough — когда запаса хватит на need. Второе значение ложно, если
// не хватит никогда: запас не растёт или тает.
func (r Resource) Enough(need float64, now time.Time) (time.Time, bool) {
	if r.AmountAt(now) >= need {
		return now, true
	}
	if r.Rate <= 0 {
		return time.Time{}, false
	}
	secs := (need - r.Amount) / r.Rate
	return r.Measured.Add(time.Duration(secs * float64(time.Second))), true
}

// ArmyUnit — одна пачка юнитов одного типа в армии.
type ArmyUnit struct {
	Type int
	Size int
	// HasNames — у юнита есть список имён (у героини он приходит пустым).
	// Игра ждёт это поле обратно ровно там, где сама его прислала.
	HasNames bool
}

// Army — армия на карте. Полей у неё втрое больше, но нам хватает тех,
// что игра требует вернуть вместе с командой.
type Army struct {
	ID       int64
	Owner    int
	Size     int
	Location int
	Units    []ArmyUnit
	// Deploying — армия уже занята размещением: у неё стоит команда «dwc»,
	// то есть героиня призывает пехоту и до конца срока неподвижна.
	// DeployLeft — сколько осталось по часам игрового сервера; они идут
	// быстрее настоящих ровно во столько раз, во сколько ускорена партия.
	Deploying  bool
	DeployLeft time.Duration
}

// Player — игрок партии: страна, имя на сайте и что с ним стало.
// Color — цвет страны на карте, в виде готовой CSS-строки.
// SiteUserID — его номер на сайте игры: тот самый «игровой ID», который
// вписывают в карточку, поэтому по нему партия и сводится с базой.
type Player struct {
	ID         int
	Nation     string
	Name       string
	SiteUserID string
	Color      string
	// TeamID — коалиция игрока в этой партии. Ноль означает «сам по себе»:
	// так игра помечает всех, кто ни в какой коалиции не состоит.
	TeamID int
	// Premium — подписка «Высокое командование» у игрока. Игра сообщает
	// её про всех участников партии, а не только про нас, и знать это
	// полезно: премиум даёт и лишний слот производства, и ускорения.
	Premium bool
	// Banned — аккаунт игрока забанен. Это свойство аккаунта, а не партии,
	// поэтому его и стоит запоминать: в следующий раз человек может нам
	// и не встретиться, а знать о бане будет полезно.
	Banned   bool
	IsAI     bool
	Defeated bool
	Retired  bool
}

// Team — коалиция партии. Своё название и свой цвет игра даёт каждой,
// поэтому придумывать их не приходится. Распущенные коалиции сюда
// не попадают: игра держит их в списке, но состоять в них уже нельзя.
type Team struct {
	ID    int
	Name  string
	Color string
}

// GameState — то, что мы забрали с игрового сервера за один заход.
type GameState struct {
	GameID string
	// MapID — какая карта под партией: по нему берутся очертания провинций.
	MapID string
	Day   int
	// Anonymous — анонимный раунд: игра подменяет имена участников, а номера
	// аккаунтов оставляет настоящими. То же, что Game.IsAnonymous у сайта.
	Anonymous bool
	Me        int // наш playerID в этой партии
	Players   map[int]Player
	// Teams — коалиции партии по своему номеру. Пусто, если коалиций
	// в партии нет: игроки тогда все с TeamID = 0.
	Teams     map[int]Team
	Provinces []Province
	Armies    []Army
	// HeroTypes — типы юнитов, умеющих призывать пехоту (роль
	// DEPLOY_INFANTRY), и их имена: по ним ищется армия с героиней.
	HeroTypes map[int]string
	// Upgrades — справочник зданий этой партии по номеру.
	Upgrades map[int]Upgrade
	// Resources — наши запасы по номеру ресурса. У наблюдателя пусто:
	// чужие запасы игра не рассказывает.
	Resources map[int]Resource
}

// Province возвращает провинцию по номеру.
func (g *GameState) Province(id int) (Province, bool) {
	for _, p := range g.Provinces {
		if p.ID == id {
			return p, true
		}
	}
	return Province{}, false
}

// DeployArmy — наша армия с героиней, умеющей призыв пехоты. Пусто, если
// такой армии в партии нет: героиню в партию ещё не отправили или её убили.
// У наблюдателя своего игрока нет вовсе, и своей армии у него быть не может:
// иначе за нас сошёл бы владелец с номером ноль.
func (g *GameState) DeployArmy() (Army, string, bool) {
	if g.Me <= 0 {
		return Army{}, "", false
	}
	for _, a := range g.Armies {
		if a.Owner != g.Me {
			continue
		}
		for _, u := range a.Units {
			if name, ok := g.HeroTypes[u.Type]; ok {
				return a, name, true
			}
		}
	}
	return Army{}, "", false
}

// Owned возвращает провинции игрока в том порядке, в каком их отдала игра.
func (g *GameState) Owned(playerID int) []Province {
	var out []Province
	for _, p := range g.Provinces {
		if p.Owner == playerID {
			out = append(out, p)
		}
	}
	return out
}

// gameAccess — разовый пропуск на игровой сервер конкретной партии.
type gameAccess struct {
	host   string
	auth   string
	tstamp string
}

// GameState заходит в партию игроком и забирает её состояние целиком.
// Такой заход игра засчитывает как вход в партию, и работает он только
// там, где мы играем. Для чужой партии есть ObserveGame.
func (c *Client) GameState(ctx context.Context, gameID string) (*GameState, error) {
	_, state, err := c.enter(ctx, gameID)
	return state, err
}

// ObserveGame забирает состояние партии наблюдателем: активацию мы
// пропускаем, а с ней — и вход в партию. Игра отдаёт всё то же самое,
// включая коалиции с их названиями и цветами, про любую партию, играем мы
// в ней или нет; своего игрока в таком состоянии нет, поэтому Me = 0.
func (c *Client) ObserveGame(ctx context.Context, gameID string) (*GameState, error) {
	acc, err := c.gameAccess(ctx, gameID)
	if err != nil {
		return nil, err
	}
	return c.gameState(ctx, acc, gameID, 1, 0)
}

// enter проходит вход в партию и возвращает пропуск вместе с состоянием:
// пропуск нужен тем, кто после этого шлёт команды.
func (c *Client) enter(ctx context.Context, gameID string) (*gameAccess, *GameState, error) {
	acc, err := c.gameAccess(ctx, gameID)
	if err != nil {
		return nil, nil, err
	}

	// Активация выдаёт наш номер в партии; она же и есть тот самый вход,
	// который игра запоминает.
	var playerID int
	if err := c.gsCall(ctx, acc, gameID, 1, "ultshared.action.UltActivateGameAction", 0, map[string]any{
		"selectedPlayerID":              -1,
		"selectedTeamID":                -1,
		"randomTeamAndCountrySelection": false,
		"os":                            "Linux ",
		"device":                        "desktop",
		"isMobileClient":                true,
	}, &playerID); err != nil {
		return nil, nil, err
	}
	if playerID <= 0 {
		return nil, nil, fmt.Errorf("вход в партию %s: игра вернула playerID=%d (мы в ней не играем?)", gameID, playerID)
	}

	state, err := c.gameState(ctx, acc, gameID, 2, playerID)
	if err != nil {
		return nil, nil, err
	}
	return acc, state, nil
}

// gameState спрашивает у игрового сервера полное состояние партии.
// playerID = 0 означает «смотрим наблюдателем»: игра такой запрос принимает
// и без активации, только своего игрока в ответе тогда нет.
func (c *Client) gameState(ctx context.Context, acc *gameAccess, gameID string,
	reqID, playerID int) (*GameState, error) {

	var state gameStateResponse
	if err := c.gsCall(ctx, acc, gameID, reqID, "ultshared.action.UltUpdateGameStateAction", playerID, map[string]any{
		"actions": []any{map[string]any{
			"requestID":  "actionReq-1",
			"@c":         "ultshared.action.UltLoginAction",
			"resolution": "1920x1080",
			"sysInfos": map[string]any{
				"@c":             "ultshared.action.UltSystemInfos",
				"os":             "Linux ",
				"device":         "desktop",
				"browser":        "chrome",
				"isMobileClient": true,
				"assetQuality":   "high",
				"mapQuality":     "high",
				"gpuQuality":     "high",
				"memoryQuality":  "high",
				"source":         trackingSource,
			},
		}},
		"lastCallDuration": 0,
	}, &state); err != nil {
		return nil, err
	}
	state.received = time.Now()

	built, err := state.build(gameID, playerID)
	if err != nil {
		return nil, err
	}
	// Единственное место, где состояние приезжает с игрового сервера, —
	// значит, и единственное, где кэш надо наполнять. Кладут сюда все
	// разом: и страница, и сборщик коалиций, и автопилот.
	c.cacheState(gameID, built)
	return built, nil
}

// gameAccess спрашивает у сайта, на каком сервере идёт партия и с каким
// токеном туда пускают. Ответ — та же пачка параметров, которую сайт
// подставляет в адрес клиента игры, поэтому берём из неё только нужное.
func (c *Client) gameAccess(ctx context.Context, gameID string) (*gameAccess, error) {
	raw, err := c.call(ctx, "getGameToken", []param{{"gameID", gameID}})
	if err != nil {
		return nil, err
	}

	var res struct {
		Token struct {
			GS     string `json:"gs"`
			Auth   string `json:"auth"`
			Tstamp string `json:"authTstamp"`
		} `json:"token"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор токена партии: %w", err)
	}
	if res.Token.GS == "" || res.Token.Auth == "" {
		return nil, fmt.Errorf("в токене партии %s нет адреса сервера или ключа", gameID)
	}
	return &gameAccess{host: res.Token.GS, auth: res.Token.Auth, tstamp: res.Token.Tstamp}, nil
}

// gsCall отправляет одно действие игровому серверу и разбирает поле result.
// Общие поля запроса игра требует в каждом действии, поэтому они собираются
// здесь, а вызывающий добавляет только своё.
func (c *Client) gsCall(ctx context.Context, acc *gameAccess, gameID string, reqID int,
	class string, playerID int, extra map[string]any, out any) error {

	// Вторая попытка нужна ровно на один случай: игра сказала, какой версией
	// клиента с ней теперь разговаривают. Запоминаем её и повторяем запрос.
	for attempt := 0; ; attempt++ {
		raw, err := c.gsSend(ctx, acc, gameID, reqID, class, playerID, extra)
		if err != nil {
			return err
		}

		var exc struct {
			Class  string `json:"@c"`
			Detail string `json:"detailMessage"`
		}
		if json.Unmarshal(raw, &exc) == nil && strings.HasSuffix(exc.Class, "Exception") {
			e := &gsException{class: exc.Class, detail: exc.Detail}
			if v := e.version(); v != "" && attempt == 0 {
				c.log.Warn("игровой сервер ждёт другую версию клиента", "версия", v)
				c.setGameVersion(v)
				continue
			}
			return fmt.Errorf("%s: %w", class, e)
		}

		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s: разбор ответа: %w", class, err)
		}
		return nil
	}
}

// gsSend отправляет одно действие и возвращает поле result как есть.
// Общие поля запроса игра требует в каждом действии, поэтому они собираются
// здесь, а вызывающий добавляет только своё.
func (c *Client) gsSend(ctx context.Context, acc *gameAccess, gameID string, reqID int,
	class string, playerID int, extra map[string]any) (json.RawMessage, error) {

	body := map[string]any{
		"requestID":  reqID,
		"@c":         class,
		"version":    c.gameVersion(),
		"client":     gsClient,
		"siteUserID": numeric(c.UserID()),
		"adminLevel": 0,
		"gameID":     numeric(gameID),
		"playerID":   playerID,
		"rights":     "chat",
		"userAuth":   acc.auth,
		"tstamp":     acc.tstamp,
	}
	for k, v := range extra {
		body[k] = v
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+acc.host+"/", strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	// Клиент игры ходит сюда именно так: тело — JSON, а тип заявлен формой.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", class, err)
	}
	defer resp.Body.Close()

	// Состояние партии — это мегабайты, поэтому лимит здесь щедрее обычного.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: чтение ответа: %w", class, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Action: class, Status: resp.StatusCode}
	}

	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{Action: class, Malformed: true, Message: err.Error()}
	}
	if len(env.Result) == 0 {
		return nil, &APIError{Action: class, Malformed: true, Message: "в ответе нет поля result"}
	}
	return env.Result, nil
}

// gameStateResponse — только те части состояния, которые нам нужны.
// Ключи состояний игра нумерует: 1 — игроки, 3 — карта, 12 — общая информация.
type gameStateResponse struct {
	// TimeStamp — часы игрового сервера на момент ответа. По ним считается
	// остаток размещения: сравнивать с нашими часами нельзя, игровое время
	// идёт быстрее.
	TimeStamp string `json:"timeStamp"`
	// received — наши часы в миг ответа. Вместе с TimeStamp и началом
	// партии по ним узнаётся её скорость (см. gameClock). Ноль — часы
	// не известны, и время партии берётся как есть.
	received time.Time
	States   struct {
		Players struct {
			Players map[string]struct {
				PlayerID   int    `json:"playerID"`
				NationName string `json:"nationName"`
				Color      string `json:"primaryColor"`
				UserName   string `json:"userName"`
				SiteUserID int64  `json:"siteUserID"`
				CapitalID  int    `json:"capitalID"`
				TeamID     int    `json:"teamID"`
				Premium    bool   `json:"premiumUser"`
				Banned     bool   `json:"banned"`
				IsAI       bool   `json:"computerPlayer"`
				Defeated   bool   `json:"defeated"`
				Retired    bool   `json:"retired"`
			} `json:"players"`
			// Коалиции лежат рядом с игроками: у игрока только номер
			// коалиции, а название, цвет и роспуск — здесь.
			Teams map[string]struct {
				TeamID    int    `json:"teamID"`
				Name      string `json:"name"`
				Color     string `json:"primaryColor"`
				Disbanded bool   `json:"disbanded"`
			} `json:"teams"`
		} `json:"1"`
		Map struct {
			Map struct {
				MapID     string `json:"mapID"`
				Locations []struct {
					Class  string   `json:"@c"`
					ID     int      `json:"id"`
					Name   string   `json:"n"`
					Owner  *int     `json:"o"`
					Morale *float64 `json:"m"`
					// us — построенные здания, cos — список идущих строек,
					// bi — первая из них же. Читаем список: со вторым
					// слотом (премиум) строек бывает две, и в bi лежит
					// только одна.
					Built []struct {
						ID int `json:"id"`
					} `json:"us"`
					Constructions json.RawMessage `json:"cos"`
					Building      *buildWire      `json:"bi"`
					Slots         int             `json:"cs"`
				} `json:"locations"`
			} `json:"map"`
		} `json:"3"`
		Armies struct {
			Armies map[string]struct {
				ID       int64 `json:"id"`
				Owner    *int  `json:"o"`
				Size     int   `json:"s"`
				Location int   `json:"l"`
				Units    []struct {
					Type  int       `json:"t"`
					Size  int       `json:"s"`
					Names *[]string `json:"n"`
				} `json:"u"`
				Commands []struct {
					Class    string `json:"@c"`
					ExecTime int64  `json:"execTime"`
				} `json:"c"`
			} `json:"armies"`
		} `json:"6"`
		Mod struct {
			// allUnitTypes — полный справочник; unitTypes в партии урезан
			// и героини в нём может не быть.
			AllUnitTypes map[string]struct {
				UnitTypeID   int    `json:"unitTypeId"`
				UnitName     string `json:"unitName"`
				RatingConfig struct {
					UnitRoles []string `json:"unitRoles"`
				} `json:"ratingConfig"`
			} `json:"allUnitTypes"`
			// upgrades — здания партии: название на языке аккаунта (upn),
			// время стройки в секундах (bt) и цена по ресурсам (c).
			Upgrades map[string]struct {
				ID        int                `json:"id"`
				Name      string             `json:"upn"`
				BuildTime int                `json:"bt"`
				Cost      map[string]float64 `json:"c"`
				// ru — здание, которое это заменяет собой: так игра
				// связывает уровни одного и того же здания.
				Replaces flexInt `json:"ru"`
			} `json:"upgrades"`
		} `json:"11"`
		// Ресурсы: запас на момент time0 и прирост в секунду. Лежат они
		// по игрокам, но чужие профили игра не наполняет — берём свой.
		Resources struct {
			Profiles map[string]struct {
				Categories map[string]struct {
					Entries map[string]struct {
						ID       int     `json:"resourceID"`
						Name     string  `json:"name"`
						Amount   float64 `json:"amount0"`
						Measured int64   `json:"time0"`
						Rate     float64 `json:"rate"`
					} `json:"resourceEntries"`
				} `json:"categories"`
			} `json:"resourceProfs"`
		} `json:"4"`
		Info struct {
			DayOfGame int `json:"dayOfGame"`
			// StartOfGame — начало партии, в секундах. Это настоящее время:
			// от него игровые часы и начинают бежать быстрее.
			StartOfGame int64 `json:"startOfGame"`
			// Свойства партии: клиент игры узнаёт анонимный раунд именно
			// отсюда, по включённой фиче с номером featureAnonymous.
			GameFeatures struct {
				IDFeatures map[string]struct {
					Enabled bool `json:"enabled"`
				} `json:"idFeatures"`
			} `json:"gameFeatures"`
		} `json:"12"`
	} `json:"states"`
}

const (
	// provinceLand — суша; «sp» в тех же списках означает море.
	provinceLand = "p"

	// roleDeployInfantry — роль юнита, который умеет призывать пехоту.
	// Так игра помечает Мейв; фичи DEPLOY_UNIT у её типа при этом нет.
	roleDeployInfantry = "DEPLOY_INFANTRY"

	// commandDeployWait — команда «идёт размещение»: игра ставит её армии
	// на всё время призыва.
	commandDeployWait = "dwc"

	// featureAnonymous — номер фичи «анонимный раунд» в свойствах партии
	// (FEATURE_ANONYMOUS в клиенте игры).
	featureAnonymous = "6"
)

// build превращает ответ игры в наш вид. Провинции без владельца пропускаем:
// это нейтральные земли и вода, а нас интересует, кто чем владеет.
func (r *gameStateResponse) build(gameID string, me int) (*GameState, error) {
	locations := r.States.Map.Map.Locations
	if len(locations) == 0 {
		return nil, fmt.Errorf("партия %s: игра не прислала карту", gameID)
	}

	// Часы сервера приходят строкой; без них остаток размещения не посчитать,
	// но и повода отказываться от всего состояния в этом нет.
	serverNow, _ := strconv.ParseInt(r.TimeStamp, 10, 64)
	clock := newGameClock(r.States.Info.StartOfGame, serverNow, r.received)

	g := &GameState{
		GameID: gameID,
		MapID:  r.States.Map.Map.MapID,
		Day:    r.States.Info.DayOfGame,
		// Анонимность — то же свойство, что сайт отдаёт как anonymousRound,
		// но приходит оно с самой партией, и ждать сайт ради него не нужно.
		Anonymous: r.States.Info.GameFeatures.IDFeatures[featureAnonymous].Enabled,
		Me:        me,
		Players:   make(map[int]Player, len(r.States.Players.Players)),
	}

	// Распущенные коалиции пропускаем: игра оставляет их в списке, но
	// состоять в них уже нельзя, и на карте им делать нечего.
	for _, t := range r.States.Players.Teams {
		if t.TeamID <= 0 || t.Disbanded {
			continue
		}
		if g.Teams == nil {
			g.Teams = make(map[int]Team, len(r.States.Players.Teams))
		}
		g.Teams[t.TeamID] = Team{ID: t.TeamID, Name: t.Name, Color: cssColor(t.Color)}
	}

	capitals := make(map[int]bool)
	for _, p := range r.States.Players.Players {
		// Игра держит в этом списке и служебного игрока -1 («никто»).
		if p.PlayerID <= 0 {
			continue
		}
		player := Player{
			ID: p.PlayerID, Nation: p.NationName, Name: p.UserName,
			Color: cssColor(p.Color), TeamID: p.TeamID, Premium: p.Premium, Banned: p.Banned,
			IsAI: p.IsAI, Defeated: p.Defeated, Retired: p.Retired,
		}
		// У ботов номера на сайте нет — там ноль, и в базе его искать незачем.
		if p.SiteUserID > 0 {
			player.SiteUserID = strconv.FormatInt(p.SiteUserID, 10)
		}
		g.Players[p.PlayerID] = player
		if p.CapitalID > 0 {
			capitals[p.CapitalID] = true
		}
	}

	for _, l := range locations {
		// «p» — суша, «sp» — море. Ничейную сушу тоже берём: без неё карту
		// не нарисовать, а Owner = 0 честно означает «ничья».
		if l.Class != provinceLand {
			continue
		}
		pr := Province{ID: l.ID, Name: l.Name, Capital: capitals[l.ID], Slots: l.Slots}
		if l.Owner != nil {
			pr.Owner = *l.Owner
		}
		if l.Morale != nil {
			pr.Morale = *l.Morale
		}
		for _, b := range l.Built {
			if b.ID > 0 {
				pr.Built = append(pr.Built, b.ID)
			}
		}
		for _, c := range buildList(l.Constructions, l.Building, clock) {
			pr.Building = append(pr.Building, c)
		}
		g.Provinces = append(g.Provinces, pr)
	}

	g.Upgrades = make(map[int]Upgrade, len(r.States.Mod.Upgrades))
	for _, u := range r.States.Mod.Upgrades {
		if u.ID <= 0 {
			continue
		}
		up := Upgrade{ID: u.ID, Name: u.Name, Replaces: int(u.Replaces),
			Build: clock.duration(time.Duration(u.BuildTime) * time.Second)}
		if len(u.Cost) > 0 {
			up.Cost = make(map[int]float64, len(u.Cost))
			for id, amount := range u.Cost {
				if n, err := strconv.Atoi(id); err == nil {
					up.Cost[n] = amount
				}
			}
		}
		g.Upgrades[u.ID] = up
	}

	// Уровни считаются после того, как собран весь справочник: цепочка
	// «второй заменяет первый» ведёт к зданию, которого в списке могло
	// ещё не быть.
	for id, u := range g.Upgrades {
		u.Tier = upgradeTier(g.Upgrades, id, 0)
		g.Upgrades[id] = u
	}

	// Запасы — только свои: в чужих профилях игра ресурсы не присылает.
	if profile, ok := r.States.Resources.Profiles[strconv.Itoa(me)]; ok && me > 0 {
		g.Resources = make(map[int]Resource)
		for _, category := range profile.Categories {
			for _, e := range category.Entries {
				g.Resources[e.ID] = Resource{
					ID: e.ID, Name: e.Name, Amount: e.Amount,
					Measured: clock.real(e.Measured), Rate: e.Rate / clock.scale,
				}
			}
		}
	}

	g.HeroTypes = make(map[int]string)
	for _, u := range r.States.Mod.AllUnitTypes {
		for _, role := range u.RatingConfig.UnitRoles {
			if role == roleDeployInfantry {
				g.HeroTypes[u.UnitTypeID] = u.UnitName
			}
		}
	}

	for _, a := range r.States.Armies.Armies {
		if a.Owner == nil {
			continue
		}
		army := Army{ID: a.ID, Owner: *a.Owner, Size: a.Size, Location: a.Location}
		for _, u := range a.Units {
			army.Units = append(army.Units, ArmyUnit{Type: u.Type, Size: u.Size, HasNames: u.Names != nil})
		}
		for _, cmd := range a.Commands {
			if cmd.Class != commandDeployWait {
				continue
			}
			army.Deploying = true
			if left := cmd.ExecTime - serverNow; serverNow > 0 && left > 0 {
				army.DeployLeft = clock.duration(time.Duration(left) * time.Millisecond)
			}
		}
		g.Armies = append(g.Armies, army)
	}
	return g, nil
}

// upgradeTier — какой это уровень здания: первый, если оно ничего
// не заменяет, иначе на единицу выше заменяемого. depth страхует от
// кольца в данных: справочник приходит от игры, и проверять его нам нечем.
func upgradeTier(all map[int]Upgrade, id, depth int) int {
	u, ok := all[id]
	if !ok || u.Replaces == 0 || depth > 10 {
		return 1
	}
	return upgradeTier(all, u.Replaces, depth+1) + 1
}

// flexInt — число, которое игра шлёт то числом, то строкой. Чужой формат
// молча считаем нулём: из-за одного поля терять всё состояние партии
// нельзя, а без уровня здания список лишь станет менее внятным.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	text := strings.Trim(string(b), `"`)
	if n, err := strconv.Atoi(text); err == nil {
		*f = flexInt(n)
	}
	return nil
}

// CSSColor — то же, что cssColor, для цветов, которые приходят не с игрового
// сервера, а из клиента игры в браузере (расширение Chrome): формат у них
// тот же самый.
func CSSColor(c string) string { return cssColor(c) }

// cssColor переводит цвет страны из игрового «rgba(230,190,140,255)»
// в то, что понимает браузер. Непрозрачность игра всегда шлёт полной,
// поэтому четвёртый компонент отбрасываем.
func cssColor(c string) string {
	inside, ok := strings.CutPrefix(c, "rgba(")
	if !ok {
		return ""
	}
	inside, ok = strings.CutSuffix(inside, ")")
	if !ok {
		return ""
	}
	parts := strings.Split(inside, ",")
	if len(parts) < 3 {
		return ""
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return "rgb(" + strings.Join(parts[:3], ",") + ")"
}

// numeric приводит числовые идентификаторы, которые сайт отдаёт строками,
// к числам: игровой сервер ждёт именно числа.
func numeric(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// gameClock переводит время партии в настоящее. В ускоренной партии
// (×2, ×4, …) игра отдаёт все сроки — конец стройки, замер запасов —
// по своим часам, которые от начала партии бегут во столько же раз
// быстрее. Клиент игры считает так же (getRealTime):
// настоящее = начало + (игровое − начало) · scale.
//
// Сам масштаб в состоянии не приходит, но выводится из него: игровые
// часы в миг ответа ушли от начала во столько раз дальше наших. Скорости
// у партий целые, поэтому отношение округляется — неточность наших часов
// на несколько секунд его не сбивает уже через минуту после старта.
type gameClock struct {
	start time.Time // начало партии
	scale float64   // сколько настоящих секунд в игровой; 1 — обычная партия
}

func newGameClock(startSec, serverNowMs int64, received time.Time) gameClock {
	c := gameClock{start: time.Unix(startSec, 0), scale: 1}
	if startSec <= 0 || serverNowMs <= 0 || received.IsZero() {
		return c
	}
	real := received.Sub(c.start)
	game := time.UnixMilli(serverNowMs).Sub(c.start)
	if real < time.Minute || game <= 0 {
		return c
	}
	if speed := math.Round(float64(game) / float64(real)); speed > 1 {
		c.scale = 1 / speed
	}
	return c
}

// real — настоящее время для игровых миллисекунд.
func (c gameClock) real(ms int64) time.Time {
	t := time.UnixMilli(ms)
	if c.scale == 1 {
		return t
	}
	return c.start.Add(time.Duration(float64(t.Sub(c.start)) * c.scale))
}

// duration — сколько длится игровой отрезок по настоящим часам.
func (c gameClock) duration(d time.Duration) time.Duration {
	return time.Duration(float64(d) * c.scale)
}
