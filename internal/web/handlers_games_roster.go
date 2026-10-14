package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// mapTicketTTL — сколько после показа карты можно дорисовать её составом.
// С запасом на самое долгое ожидание сайта: страница просит состав сразу,
// как загрузилась, а сайт отдаёт большую партию до минуты.
//
// lossesTicketTTL — то же для вкладки «Потери». Её жмут не сразу, а когда
// дойдут до неё, разглядывая карту, — пять минут тут мало. Хранится пропуск
// до большего из сроков; каждый запрос сверяет свой.
const (
	mapTicketTTL    = 5 * time.Minute
	lossesTicketTTL = time.Hour
)

// mapTickets помнит, кому и какую карту только что показали. Запрос состава
// рисует карту заново из кэша, и без такой памяти её можно было бы получить
// по прямой ссылке, не потратив проверки: пропуск выдаёт только страница,
// которая проверку уже взяла.
//
// Живёт в памяти: пропуск нужен на минуты, и перезапуск, стерев его, отнимет
// у человека лишь дорисовку — карта у него уже есть.
type mapTickets struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newMapTickets() *mapTickets {
	return &mapTickets{seen: make(map[string]time.Time)}
}

func mapTicketKey(userID int64, gameID string) string {
	return strconv.FormatInt(userID, 10) + ":" + gameID
}

// grant выдаёт пропуск и заодно выбрасывает протухшие: выдают их столько же,
// сколько показывают карт, и отдельная уборка тут не нужна.
func (t *mapTickets) grant(userID int64, gameID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, at := range t.seen {
		if now.Sub(at) > lossesTicketTTL {
			delete(t.seen, k)
		}
	}
	t.seen[mapTicketKey(userID, gameID)] = now
}

// has — показывали ли этому человеку эту карту не дольше ttl назад.
// Пропуск не гасится: сайт может не успеть, и повторить запрос человек
// вправе, пока срок не вышел.
func (t *mapTickets) has(userID int64, gameID string, ttl time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.seen[mapTicketKey(userID, gameID)]
	return ok && time.Since(at) <= ttl
}

// gameRoster — вторая половина страницы большой партии. Страница нарисовала
// карту по состоянию и кланам из базы, не дожидаясь сайта; этот запрос
// дожидается его и отдаёт блок карты заново — с составом и кланами от сайта.
// Проверку он не тратит и в партию не ходит: состояние берётся из кэша,
// а право на него страница подтвердила пропуском.
//
// Сбой здесь страницу не ломает: карта остаётся, а на месте строки
// «догружаем состав» появляется объяснение.
func (s *Server) gameRoster(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	who := currentUser(r)
	lang := langOf(r)

	if s.games == nil || !s.mapTickets.has(who.ID, gameID, mapTicketTTL) {
		s.rosterFailed(w, r, lang.T("game.roster.expired"))
		return
	}
	state, ok := s.games.CachedState(gameID)
	if !ok {
		s.rosterFailed(w, r, lang.T("game.roster.expired"))
		return
	}
	if state.Anonymous && !who.CanSeeAnonymousMaps() {
		s.rosterFailed(w, r, lang.T("game.anonymous.closed"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.gameBudget(gameID))
	defer cancel()

	game, roster, err := s.games.Game(ctx, gameID)
	if err != nil {
		s.log.Warn("состав партии", "gameID", gameID, "err", err)
		msg := lang.T("game.roster.error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			msg = lang.T("game.roster.slow", waitSeconds(s.gameBudget(gameID)))
		}
		// Повторить можно: пропуск ещё действует, а сайт со второго раза
		// обычно успевает.
		s.renderRosterFailed(w, r, map[string]any{"Message": msg, "Retry": true, "GameID": gameID})
		return
	}
	if game.IsAnonymous() && !who.CanSeeAnonymousMaps() {
		s.rosterFailed(w, r, lang.T("game.anonymous.closed"))
		return
	}

	data := map[string]any{"GameID": gameID, "State": state}
	if err := s.fillLive(ctx, r, data, state, roster, false); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Ответ заменяет карту целиком, и пустой ответ её бы просто стёр.
	// Очертания лежат в кэше, так что это почти невозможно — но уж если,
	// пусть останется карта без состава.
	if data["Map"] == nil {
		s.rosterFailed(w, r, data["MapError"].(string))
		return
	}

	// Шапку страницы чужой партии дописываем тем же ответом: название ей
	// называет только сайт. Своя его уже знает из списка своих — и знает
	// больше, вплоть до нашего номера в партии, поэтому её не трогаем.
	// Список своих лежит в кэше; не ответил — шапка просто останется.
	if games, err := s.games.MyGames(ctx); err == nil && !hasGame(games, gameID) {
		view := gameViews(lang, []supremacy.Game{*game})[0]
		data["Game"] = &view
		data["HeadSwap"] = true
	}

	s.renderPartial(w, r, "game", "gamemap", data)
}

func hasGame(games []supremacy.Game, gameID string) bool {
	for _, g := range games {
		if g.GameID == gameID {
			return true
		}
	}
	return false
}

// rosterFailed ставит объяснение на место строки «догружаем состав». Код
// ответа обычный: htmx на ошибку ничего не вставляет, а сказать человеку,
// почему таблицы не будет, нужно.
func (s *Server) rosterFailed(w http.ResponseWriter, r *http.Request, msg string) {
	s.renderRosterFailed(w, r, map[string]any{"Message": msg})
}

func (s *Server) renderRosterFailed(w http.ResponseWriter, r *http.Request, data map[string]any) {
	w.Header().Set("HX-Retarget", "#roster-later")
	w.Header().Set("HX-Reswap", "outerHTML")
	s.renderPartial(w, r, "game", "rosterfailed", data)
}

// rosterView — строка состава партии: то, что сайт рассказывает про
// участника, плюс то, что о нём знаем мы. Card — карточка из базы, если
// игрок в ней есть; Enemy и Friend — личные списки смотрящего.
//
// Team — номер коалиции, как его отдаёт сайт. Названия коалиций он не даёт,
// они видны только изнутри партии, поэтому номер здесь работает как метка
// «эти трое заодно».
type rosterView struct {
	Login string
	// GameID — номер аккаунта на сайте игры. Тот же, которым карточка
	// опознаётся в базе, и по нему же человека ищут руками.
	GameID string
	// Nation — за кого играет. Известна только из состояния партии:
	// сайт про страны не рассказывает.
	Nation string
	Clan   string
	Team   string
	Level  int
	// Stats — боевой счёт с сайта игры: уровень, кд, партии, победы.
	// Не про всех: страница показывает уже собранное, см. gameStats.
	Stats domain.UserStats
	// Power — та же полоса, что красит карту в режиме «Сила»: насколько
	// человек опаснее смотрящего. Пусто, когда сравнивать не с чем —
	// у него нет счёта, у нас нет своего или мы ещё не воевали.
	Power string
	// Page — на какой странице таблицы стоит строка. Считается после
	// сортировки; ноль означает, что страниц нет и показывают всё сразу.
	Page   int
	Card   *domain.Player
	Enemy  bool
	Friend bool
}

// rosterPageSize — сколько строк состава на одной странице таблицы,
// rosterPaged — с какого числа строк её вообще делят на страницы.
//
// Страницы переключаются радиокнопками и css, без походов на сервер:
// страница партии перезагружается только вместе с показом карты, а каждый
// такой показ стоит проверки из дневной квоты. Листать таблицу за свои
// проверки человек не должен.
const (
	rosterPageSize = 50
	rosterPaged    = supremacy.BigRoster
)

// rosterViews готовит состав партии к показу и сводит его с базой по
// игровому ID: тот самый номер, что сайт зовёт siteUserID. Порядок — по
// силе, самые опасные сверху; кого не спрашивали, те в конце.
//
// Боевой счёт читается из базы и в сеть не ходит: его собирает показ карты
// (gameStats), а здесь мы показываем уже собранное; у неспрошенных будут
// прочерки.
// Второе значение — есть ли счёт у самого смотрящего. Без него колонка силы
// пуста целиком, и сказать об этом надо словами: пустой столбец читается
// как поломка, а не как «сравнивать не с чем».
func (s *Server) rosterViews(r *http.Request, roster []supremacy.GameLogin) ([]rosterView, bool, error) {
	lang := langOf(r)
	if len(roster) == 0 {
		return nil, false, nil
	}

	ids := make([]string, 0, len(roster))
	for _, l := range roster {
		if l.Known() {
			ids = append(ids, l.SiteUserID)
		}
	}
	cards, err := s.players.ByGameIDs(r.Context(), ids)
	if err != nil {
		return nil, false, err
	}
	// Счёт смотрящего идёт в той же пачке: без него «опаснее» и «слабее»
	// не значат ничего, и вся колонка силы остаётся пустой.
	me := currentUser(r)
	stats, err := s.userStats.Known(r.Context(), append(ids, me.GameID))
	if err != nil {
		return nil, false, err
	}
	list := make([]*domain.Player, 0, len(cards))
	for _, c := range cards {
		list = append(list, c)
	}
	enemies, err := s.enemies.marked(r, list)
	if err != nil {
		return nil, false, err
	}
	friends, err := s.friends.marked(r, list)
	if err != nil {
		return nil, false, err
	}

	out := make([]rosterView, 0, len(roster))
	for _, l := range roster {
		view := rosterView{Login: l.Login, Level: l.Level, GameID: l.SiteUserID}
		if l.Known() {
			view.Stats = stats[l.SiteUserID]
			view.Power = powerClass(view.Stats, stats[me.GameID])
		}
		if l.InClan() {
			view.Clan = nonEmpty(l.AllianceName, lang.T("tip.clan", l.AllianceID))
		}
		if l.TeamID != "" && l.TeamID != "0" {
			view.Team = l.TeamID
		}
		if card, ok := cards[l.SiteUserID]; ok {
			view.Card = card
			view.Enemy, view.Friend = enemies[card.ID], friends[card.ID]
		}
		out = append(out, view)
	}

	finishRoster(out)
	return out, stats[me.GameID].Rated(), nil
}

// finishRoster ставит состав в порядок и раскладывает по страницам.
// Зовётся дважды: сразу после сборки и ещё раз, когда приедет состояние
// партии и счёт станет полнее, — порядок-то от счёта и зависит.
func finishRoster(list []rosterView) {
	sortRoster(list)
	// Страницы нумеруются после сортировки: они про место в готовом
	// списке, а не про игрока.
	for i := range list {
		if len(list) >= rosterPaged {
			list[i].Page = i/rosterPageSize + 1
		} else {
			list[i].Page = 0
		}
	}
}

// rosterPages — номера страниц таблицы состава, по порядку. Пусто, когда
// делить не на что: шаблону тогда нечего рисовать, и переключателя не будет.
func rosterPages(list []rosterView) []int {
	if len(list) < rosterPaged {
		return nil
	}
	n := (len(list) + rosterPageSize - 1) / rosterPageSize
	pages := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		pages = append(pages, i)
	}
	return pages
}

// rosterFromState дописывает в состав то, что знает только состояние партии:
// страны участников и свежий боевой счёт.
//
// Счёт дописывается не из вредности. Показ карты по дороге сам дособирает
// счёт тех, кого давно не спрашивали (см. gameStats), и ставит в таблицу
// именно его — тот же, которым покрашена карта. Без этого шага таблица
// и карта могли бы разойтись, если запись в базу не удалась.
//
// Страна нужна, чтобы таблицу можно было связать с картой глазами: на карте
// видно страну, а не ник. Без карты её нет вовсе, и это не ошибка.
//
// Порядок после этого пересобирается: он считается от счёта.
func rosterFromState(list []rosterView, state *supremacy.GameState, sides *gameSides) {
	if state == nil {
		return
	}
	nations := make(map[string]string, len(state.Players))
	stats := make(map[string]domain.UserStats, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID == "" {
			continue
		}
		if p.Nation != "" {
			nations[p.SiteUserID] = p.Nation
		}
		if sides != nil {
			if st, ok := sides.Stats[p.ID]; ok {
				stats[p.SiteUserID] = st
			}
		}
	}
	for i := range list {
		list[i].Nation = nations[list[i].GameID]
		if st, ok := stats[list[i].GameID]; ok {
			list[i].Stats = st
			if sides != nil {
				list[i].Power = powerClass(st, sides.Me)
			}
		}
	}
	finishRoster(list)
}

// sortRoster ставит состав в том порядке, в котором его читают: сначала
// те, кого стоит бояться. Кого сайт ещё не спрашивал, те уходят в конец —
// не потому, что безобидны, а потому, что про них нечего сказать, и держать
// их наверху значило бы прятать за ними опасных.
//
// Внутри равных — по нику, чтобы порядок не плясал от запуска к запуску.
func sortRoster(list []rosterView) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Stats.Rated() != b.Stats.Rated() {
			return a.Stats.Rated()
		}
		if d := a.Stats.Danger() - b.Stats.Danger(); d != 0 {
			return d > 0
		}
		return strings.ToLower(a.Login) < strings.ToLower(b.Login)
	})
}
