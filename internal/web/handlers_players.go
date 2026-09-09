package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

const searchLimit = 50

// home — стартовый экран поиска.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	query, status, traits := searchParams(r)
	players, err := s.findPlayers(r, query, status, traits)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	total, err := s.players.Count(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Справочник нужен на самой странице: галочки фильтра — это он и есть.
	// Неактивные не показываем, но уже проставленные отметки живы, поэтому
	// в карточках они остаются.
	all, err := s.traits.List(r.Context(), true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	marks, err := s.marks(r, players)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", merge(marks, map[string]any{
		"Query": query, "Status": string(status), "Players": players,
		"Traits": all, "Selected": chosen(traits), "SelectedTraits": traits,
		"Total": total, "Limit": searchLimit, "CanMark": true,
	}))
}

// search отвечает на живой ввод: HTMX подменяет только список результатов.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query, status, traits := searchParams(r)
	players, err := s.findPlayers(r, query, status, traits)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	marks, err := s.marks(r, players)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPartial(w, r, "home", "results", merge(marks, map[string]any{
		"Query": query, "Status": string(status), "SelectedTraits": traits,
		"Players": players, "Limit": searchLimit, "CanMark": true,
	}))
}

// marks — кто из выборки уже в личных списках смотрящего. Строка поиска
// показывает обе пометки сразу, поэтому и считаются они вместе.
func (s *Server) marks(r *http.Request, players []*domain.Player) (map[string]any, error) {
	enemies, err := s.enemies.marked(r, players)
	if err != nil {
		return nil, err
	}
	friends, err := s.friends.marked(r, players)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"MarkedEnemies": enemies, "MarkedFriends": friends,
		"EnemyWords": enemyWords, "FriendWords": friendWords,
	}, nil
}

// merge складывает две карты данных для шаблона: общее и то, что нужно
// конкретной странице.
func merge(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

// searchParams разбирает строку запроса. Незнакомый статус — это не ошибка,
// а «фильтр не выбран»: поиск не то место, где показывают 400. С признаками
// так же: незнакомый код просто никому не подойдёт, и ответом будет пусто.
func searchParams(r *http.Request) (string, domain.ClanStatus, []string) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	status, ok := domain.ParseClanStatus(q.Get("status"))
	if !ok {
		status = ""
	}

	var traits []string
	for _, code := range q["traits"] {
		if code = strings.TrimSpace(code); code != "" {
			traits = append(traits, code)
		}
	}
	return query, status, traits
}

// chosen — отмеченные признаки набором, чтобы шаблон не искал код в списке
// на каждой галочке.
func chosen(codes []string) map[string]bool {
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out
}

// findPlayers ищет только когда есть о чём спрашивать: пустая строка,
// невыбранный статус и ни одной галочки — это ещё не запрос, и вываливать
// в ответ на них всю базу незачем.
func (s *Server) findPlayers(r *http.Request, query string, status domain.ClanStatus,
	traits []string) ([]*domain.Player, error) {

	if query == "" && status == "" && len(traits) == 0 {
		return nil, nil
	}
	return s.players.Search(r.Context(), query, status, traits, searchLimit)
}

func (s *Server) playerCard(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	notes, err := s.players.Notes(r.Context(), player.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Что игра рассказала про этого человека, когда мы в последний раз
	// заходили в партию с ним. Ничего не рассказала — карточка просто
	// обходится без этого: связь идёт по игровому ID, а он есть не у всех.
	var seen *domain.GamePlayer
	// История банов: сегодняшний статус живёт в seen и снятие бана его
	// затирает, поэтому «сидел когда-то» видно только по журналу.
	var bans []domain.BanEvent
	if player.GameID != "" {
		known, err := s.gamePlayers.ByGameIDs(r.Context(), []string{player.GameID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if p, ok := known[player.GameID]; ok {
			seen = &p
		}
		bans, err = s.gamePlayers.BanHistory(r.Context(), player.GameID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	s.render(w, r, http.StatusOK, "player", map[string]any{
		"Player": player, "Notes": notes, "Error": "", "Seen": seen,
		"Bans": bans,
	})
}

func (s *Server) playerNew(w http.ResponseWriter, r *http.Request) {
	s.renderPlayerForm(w, r, http.StatusOK, nil, nil)
}

func (s *Server) playerEdit(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	selected := map[int64]bool{}
	for _, t := range player.Traits {
		selected[t.ID] = true
	}
	s.renderPlayerForm(w, r, http.StatusOK, player, selected)
}

// renderPlayerForm обслуживает и создание, и правку: отличаются только
// заголовком и адресом отправки.
func (s *Server) renderPlayerForm(w http.ResponseWriter, r *http.Request, status int, player *domain.Player, selected map[int64]bool, errs ...string) {
	traits, err := s.traits.List(r.Context(), true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	clans, err := s.clans.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if selected == nil {
		selected = map[int64]bool{}
	}
	msg := ""
	if len(errs) > 0 {
		msg = errs[0]
	}
	s.render(w, r, status, "player_form", map[string]any{
		"Player": player, "Traits": traits, "Clans": clans,
		"Selected": selected, "Error": msg,
	})
}

func (s *Server) playerCreate(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))
	nickname := strings.TrimSpace(r.PostFormValue("nickname"))
	clan := strings.TrimSpace(r.PostFormValue("clan"))
	traitIDs := parseIDs(r.PostForm["traits"])

	fail := func(msg string) {
		s.renderPlayerForm(w, r, http.StatusUnprocessableEntity,
			&domain.Player{GameID: gameID, Nickname: nickname, ClanName: clan}, idSet(traitIDs), msg)
	}
	// У новой карточки игровой ID обязателен: ник игрок может сменить,
	// и без ID карточку потом не опознать.
	if err := validateGameID(gameID, true); err != nil {
		fail(errText(r, err))
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(errText(r, err))
		return
	}

	player, err := s.players.Create(r.Context(), gameID, nickname, clan, traitIDs, actor.ID)
	if errors.Is(err, domain.ErrNickTaken) {
		fail(langOf(r).T("err.player.nick.taken"))
		return
	}
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail(langOf(r).T("err.player.id.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Первая заметка не обязательна, но обычно она и есть повод завести карточку.
	if body := strings.TrimSpace(r.PostFormValue("note")); body != "" {
		if _, err := s.players.AddNote(r.Context(), player.ID, actor, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	s.logAuditOn(r, "player.create", "player", player.ID,
		map[string]any{"game_id": gameID, "nickname": nickname, "clan": clan, "traits": len(traitIDs)})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

func (s *Server) playerUpdate(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))
	nickname := strings.TrimSpace(r.PostFormValue("nickname"))
	clan := strings.TrimSpace(r.PostFormValue("clan"))
	traitIDs := parseIDs(r.PostForm["traits"])

	fail := func(msg string) {
		s.renderPlayerForm(w, r, http.StatusUnprocessableEntity,
			&domain.Player{ID: player.ID, GameID: gameID, Nickname: nickname, ClanName: clan},
			idSet(traitIDs), msg)
	}
	// ID обязателен и при правке: иначе карточки, заведённые до его
	// появления, так и остались бы без него. Правка — тот самый момент,
	// когда его удобно дописать.
	if err := validateGameID(gameID, true); err != nil {
		fail(errText(r, err))
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(errText(r, err))
		return
	}

	err := s.players.Update(r.Context(), player.ID, gameID, nickname, clan, traitIDs)
	if errors.Is(err, domain.ErrNickTaken) {
		fail(langOf(r).T("err.player.nick.taken"))
		return
	}
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail(langOf(r).T("err.player.id.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditOn(r, "player.update", "player", player.ID, map[string]any{
		"game_id": gameID, "nickname": nickname, "was": player.Nickname,
		"clan": clan, "traits": len(traitIDs),
	})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

func (s *Server) playerDelete(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	if err := s.players.Delete(r.Context(), player.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "player.delete", "player", player.ID, map[string]any{"nickname": player.Nickname})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) noteCreate(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if body == "" || len([]rune(body)) > 4000 {
		notes, err := s.players.Notes(r.Context(), player.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, "player", map[string]any{
			"Player": player, "Notes": notes,
			"Error": langOf(r).T("err.note.length"),
		})
		return
	}

	id, err := s.players.AddNote(r.Context(), player.ID, currentUser(r), body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "note.create", "note", id, map[string]any{"player": player.Nickname})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

// noteDelete: свою заметку убирает автор, чужую — админ и выше.
func (s *Server) noteDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("noteID"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return
	}
	note, err := s.players.NoteByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Заметка не найдена", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	actor := currentUser(r)
	if !note.CanDelete(actor) {
		http.Error(w, "Чужую заметку может удалить только админ", http.StatusForbidden)
		return
	}
	if err := s.players.DeleteNote(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "note.delete", "note", id, map[string]any{"author": note.AuthorEmail})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(note.PlayerID, 10), http.StatusSeeOther)
}

func (s *Server) loadPlayer(w http.ResponseWriter, r *http.Request) (*domain.Player, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return nil, false
	}
	player, err := s.players.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Игрок не найден", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return player, true
}

func validateNickname(nick string) error {
	switch n := len([]rune(nick)); {
	case n == 0:
		return domain.ErrPlayerNickRequired
	case n > 64:
		return domain.ErrPlayerNickTooLong
	}
	return nil
}

// validateGameID проверяет игровой ID: он попадает в поиск наравне с ником,
// поэтому пробелы внутри не допускаются — иначе по нему не найти.
func validateGameID(gameID string, required bool) error {
	if gameID == "" {
		if required {
			return domain.ErrPlayerIDRequired
		}
		return nil
	}
	if len([]rune(gameID)) > 32 {
		return domain.ErrPlayerIDTooLong
	}
	if strings.ContainsAny(gameID, " \t\n") {
		return domain.ErrPlayerIDSpaces
	}
	return nil
}

func parseIDs(values []string) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func idSet(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
