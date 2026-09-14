package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// mapTicketTTL — сколько после показа карты можно дорисовать её составом.
// С запасом на самое долгое ожидание сайта: страница просит состав сразу,
// как загрузилась, а сайт отдаёт большую партию до минуты.
const mapTicketTTL = 5 * time.Minute

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
		if now.Sub(at) > mapTicketTTL {
			delete(t.seen, k)
		}
	}
	t.seen[mapTicketKey(userID, gameID)] = now
}

// has — показывали ли этому человеку эту карту недавно. Пропуск не гасится:
// сайт может не успеть, и повторить запрос человек вправе, пока срок не вышел.
func (t *mapTickets) has(userID int64, gameID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.seen[mapTicketKey(userID, gameID)]
	return ok && time.Since(at) <= mapTicketTTL
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

	if s.games == nil || !s.mapTickets.has(who.ID, gameID) {
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
