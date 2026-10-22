package web

import (
	"context"
	"net/http"
	"sort"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// gameRelationView — игрок партии, оказавшийся в личном списке смотрящего.
// Card — его карточка в базе, по ней и нашли. Premium — подписка
// «Высокое командование»: у союзника и у врага она одинаково важна,
// поэтому едет в оба списка.
type gameRelationView struct {
	Nation  string
	Name    string
	Premium bool
	Card    *domain.Player
}

// gamePlayerView — игрок партии сам по себе, без всякой связи с базой.
// Нужен там, где страница перечисляет участников: например, у кого
// в этой партии премиум.
type gamePlayerView struct {
	Nation string
	Name   string
	Mine   bool
}

// gameSides — что мы знаем об игроках партии помимо состояния самой партии.
// Ключ везде один: номер игрока в этой партии. Cards — карточки из базы,
// Enemies и Friends — те же карточки, отобранные личными списками
// смотрящего, Alliances — кланы игроков в самой Supremacy, по ним карта
// и красится.
type gameSides struct {
	Cards     map[int]*domain.Player
	Enemies   map[int]*domain.Player
	Friends   map[int]*domain.Player
	Alliances map[int]domain.Alliance
	// Top — верхушка рейтинга кланов по номеру клана. Общая на всю
	// админку, а не на партию: место в рейтинге принадлежит клану.
	Top map[string]domain.TopAlliance
	// Stats — боевой счёт игроков партии, Me — счёт самого смотрящего.
	// Второй нужен как точка отсчёта: режим «Сила» сравнивает участников
	// не между собой, а с тем, кто смотрит.
	Stats map[int]domain.UserStats
	Me    domain.UserStats
	// Together и TogetherLegend — режим «Играли вместе»: группа и
	// напарники каждого игрока, собранные из архива коалиций.
	Together       map[int]togetherMark
	TogetherLegend []togetherLegendView
}

// gameLists сводит игроков партии с базой по игровому ID и раскладывает их
// по личным спискам смотрящего. Карточки берутся одним запросом на всё:
// и на раскраску по кланам, и на оба списка.
func (s *Server) gameLists(r *http.Request, state *supremacy.GameState) (*gameSides, error) {
	ids := make([]string, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			ids = append(ids, p.SiteUserID)
		}
	}

	cards, err := s.players.ByGameIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	list := make([]*domain.Player, 0, len(cards))
	for _, c := range cards {
		list = append(list, c)
	}
	// Пока ручные разделы выключены, личных списков как бы нет: пустая,
	// но не nil пометка не отбирает никого, и карта красит партию одними
	// кланами — без друзей и врагов.
	enemyIDs, friendIDs := map[int64]bool{}, map[int64]bool{}
	if s.manualOn(r.Context()) {
		if enemyIDs, err = s.enemies.marked(r, list); err != nil {
			return nil, err
		}
		if friendIDs, err = s.friends.marked(r, list); err != nil {
			return nil, err
		}
	}

	// Один и тот же разбор для всех трёх наборов: игроки партии сверяются
	// с карточками, карточки — с личной пометкой. Пустая пометка означает
	// «берём все найденные карточки».
	pick := func(marked map[int64]bool) map[int]*domain.Player {
		out := make(map[int]*domain.Player)
		for _, p := range state.Players {
			card, ok := cards[p.SiteUserID]
			if !ok || (marked != nil && !marked[card.ID]) {
				continue
			}
			out[p.ID] = card
		}
		return out
	}
	return &gameSides{
		Cards:   pick(nil),
		Enemies: pick(enemyIDs),
		Friends: pick(friendIDs),
	}, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// relationViews — список для страницы: кто из партии есть в личном списке.
func relationViews(state *supremacy.GameState, found map[int]*domain.Player) []gameRelationView {
	out := make([]gameRelationView, 0, len(found))
	for playerID, card := range found {
		p := state.Players[playerID]
		out = append(out, gameRelationView{
			Nation: p.Nation, Name: p.Name, Premium: p.Premium, Card: card})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// premiumViews — у кого в партии премиум. Игра сообщает подписку про всех
// участников, и это единственное место, где её видно: в самом клиенте
// чужой премиум ничем не отмечен. Компьютерных игроков пропускаем —
// подписки у них не бывает, а список должен читаться.
func premiumViews(l i18n.Lang, state *supremacy.GameState, meID int) []gamePlayerView {
	var out []gamePlayerView
	for _, p := range state.Players {
		if !p.Premium || p.IsAI {
			continue
		}
		out = append(out, gamePlayerView{
			Nation: nonEmpty(p.Nation, l.T("game.unknown")), Name: p.Name, Mine: p.ID == meID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// seenPlayers сводит состояние партии с составом, который отдал сайт,
// и готовит то, что мы запомним про самих людей.
//
// Ник берётся из состава с сайта, а не из состояния партии. Обычно они
// совпадают, но в событийной партии с анонимным раундом игра подменяет имя
// каждого участника на «анонимный» — и состояние затёрло бы этим словом
// настоящие ники всем, кого мы уже знаем, а незнакомцам завело бы десятки
// одинаковых карточек. Сайт при этом отдаёт состав как ни в чём не бывало:
// анонимность у такой партии только на карте, номер аккаунта настоящий,
// и по нему сайт называет живого человека.
//
// Состояние остаётся запасным источником: про чужую партию сайт иногда
// молчит, и тогда известное игре имя лучше, чем никакого. Но только
// в обычной партии: в анонимной имя из состояния подменное, и ник тогда
// остаётся пустым — запись его не тронет, а карточку без ника не заведёт.
// anonymous — партия анонимная по сайту или по самому состоянию.
func seenPlayers(state *supremacy.GameState, roster []supremacy.GameLogin, anonymous bool,
	now time.Time) []domain.GamePlayer {
	byID := make(map[string]string, len(roster))
	for _, l := range roster {
		if l.Known() && l.Login != "" {
			byID[l.SiteUserID] = l.Login
		}
	}

	list := make([]domain.GamePlayer, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID == "" {
			continue
		}
		nickname := p.Name
		if anonymous || state.Anonymous {
			nickname = ""
		}
		if login, ok := byID[p.SiteUserID]; ok {
			nickname = login
		}
		list = append(list, domain.GamePlayer{
			SiteUserID: p.SiteUserID, Nickname: nickname, Banned: p.Banned,
			SeenAt: now, SeenGameID: state.GameID,
		})
	}
	return list
}

// anonymousClosed отвечает отказом на анонимную партию. Страница отдаётся
// пустой: ни состава, ни кланов, ни карты — за этим сюда и приходят.
//
// Что партия существует и что она анонимная, человек всё же узнаёт: иначе
// отказ неотличим от опечатки в номере, и человек будет набирать его снова.
// Номер он и так только что ввёл сам.
//
// Партия не идёт и в список проверенных: проверкой считается взгляд на неё,
// а взгляда не было.
func (s *Server) anonymousClosed(w http.ResponseWriter, r *http.Request, data map[string]any, gameID string) {
	s.log.Info("анонимная партия закрыта пакетом",
		"gameID", gameID, "user_id", currentUser(r).ID, "пакет", currentUser(r).Plan)
	data["AnonymousClosed"] = true
	// Код честный: это отказ в доступе, а не пустая партия. Разметку
	// с объяснением он не отменяет — её браузер покажет как обычно.
	s.render(w, r, http.StatusForbidden, "game", data)
}

// rememberPlayers записывает состав партии в базу: ник и бан у каждого,
// кто пришёл с номером на сайте. Свойства партии — поражение, выход, премиум
// — сюда не идут: они про эту игру, а не про человека, и в другой партии
// будут другими.
//
// Здесь же на каждого заводится карточка, если её ещё нет: встреченный
// в партии человек и есть тот, кого потом ищут, а заводить карточку руками
// ради ника с ID незачем. Уже заведённые не трогаются — там могут быть
// чужие правки.
//
// Ошибка записи страницу не отменяет: мы уже в партии, состав перед глазами,
// и терять его показ из-за базы было бы обидно.
func (s *Server) rememberPlayers(ctx context.Context, state *supremacy.GameState,
	roster []supremacy.GameLogin, anonymous bool) error {

	list := seenPlayers(state, roster, anonymous, time.Now())
	if err := s.gamePlayers.Save(ctx, list); err != nil {
		return err
	}

	// Новых стоит посчитать вслух: по логу видно, растёт база сама или
	// партия давно знакомая.
	added, err := s.players.ImportSeen(ctx, list)
	if err != nil {
		return err
	}
	if added > 0 {
		s.log.Info("заведены карточки из партии", "gameID", state.GameID, "новых", added)
	}

	// Уже заведённые карточки следуют за игрой: ник в них меняется на
	// нынешний, прежний уходит в историю.
	nicks := make(map[string]string, len(list))
	for _, p := range list {
		nicks[p.SiteUserID] = p.Nickname
	}
	renamed, err := s.players.RenameFromGame(ctx, nicks)
	if err != nil {
		return err
	}
	if renamed > 0 {
		s.log.Info("ники карточек обновлены из партии", "gameID", state.GameID, "сменили", renamed)
	}
	return nil
}

// bannedViews — кто из игроков партии забанен. Игра сообщает бан про всех
// участников, и это единственное место, где его видно.
func bannedViews(l i18n.Lang, state *supremacy.GameState) []gamePlayerView {
	var out []gamePlayerView
	for _, p := range state.Players {
		if !p.Banned {
			continue
		}
		out = append(out, gamePlayerView{
			Nation: nonEmpty(p.Nation, l.T("game.unknown")), Name: p.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}
