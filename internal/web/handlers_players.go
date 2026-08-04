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
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	players, err := s.findPlayers(r, query)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	total, err := s.players.Count(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", map[string]any{
		"Query": query, "Players": players, "Total": total, "Limit": searchLimit,
	})
}

// search отвечает на живой ввод: HTMX подменяет только список результатов.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	players, err := s.findPlayers(r, query)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPartial(w, r, "home", "results", map[string]any{
		"Query": query, "Players": players, "Limit": searchLimit,
	})
}

func (s *Server) findPlayers(r *http.Request, query string) ([]*domain.Player, error) {
	players, err := s.players.Search(r.Context(), query, searchLimit)
	if err != nil {
		return nil, err
	}
	return players, s.scorePlayers(r, players)
}

// scorePlayers проставляет шкалы: справочник читается один раз на запрос,
// так что изменение весов отражается сразу и без пересчёта хранимых полей.
func (s *Server) scorePlayers(r *http.Request, players []*domain.Player) error {
	if len(players) == 0 {
		return nil
	}
	all, err := s.traits.List(r.Context(), true)
	if err != nil {
		return err
	}
	for _, p := range players {
		p.Score = domain.ComputeScore(all, p.Traits)
	}
	return nil
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
	s.render(w, r, http.StatusOK, "player", map[string]any{
		"Player": player, "Notes": notes, "Error": "",
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
	clans, err := s.players.Clans(r.Context())
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
		fail(err.Error())
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(err.Error())
		return
	}

	player, err := s.players.Create(r.Context(), gameID, nickname, clan, traitIDs, actor.ID)
	if errors.Is(err, domain.ErrNickTaken) {
		fail("Игрок с таким ником уже есть в базе")
		return
	}
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail("Игрок с таким игровым ID уже есть в базе")
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
	// При правке ID можно оставить пустым: у карточек, заведённых до его
	// появления, его просто не знают.
	if err := validateGameID(gameID, false); err != nil {
		fail(err.Error())
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(err.Error())
		return
	}

	err := s.players.Update(r.Context(), player.ID, gameID, nickname, clan, traitIDs)
	if errors.Is(err, domain.ErrNickTaken) {
		fail("Игрок с таким ником уже есть в базе")
		return
	}
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail("Игрок с таким игровым ID уже есть в базе")
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
			"Error": "Заметка не может быть пустой и длиннее 4000 символов",
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
	if err := s.scorePlayers(r, []*domain.Player{player}); err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return player, true
}

func validateNickname(nick string) error {
	switch n := len([]rune(nick)); {
	case n == 0:
		return errors.New("Укажите ник игрока")
	case n > 64:
		return errors.New("Ник длиннее 64 символов")
	}
	return nil
}

// validateGameID проверяет игровой ID: он попадает в поиск наравне с ником,
// поэтому пробелы внутри не допускаются — иначе по нему не найти.
func validateGameID(gameID string, required bool) error {
	if gameID == "" {
		if required {
			return errors.New("Укажите игровой ID — по нему карточка ищется после смены ника")
		}
		return nil
	}
	if len([]rune(gameID)) > 32 {
		return errors.New("Игровой ID длиннее 32 символов")
	}
	if strings.ContainsAny(gameID, " \t\n") {
		return errors.New("Игровой ID не должен содержать пробелов")
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
