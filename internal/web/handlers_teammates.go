package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// «Играют вместе» — связи, которые админы отмечают руками. Видны всем,
// кому открыта карточка, как и комментарии; менять — админу и руту.

// teammateSearch — сколько кандидатов смотреть, разбирая введённое.
const teammateSearch = 10

// findTeammate — кого имели в виду: ID из игры точнее ника, поэтому
// сначала он. Ник не уникален: если под ним несколько карточек, просим
// указать ID, а не угадываем.
func (s *Server) findTeammate(r *http.Request, query string) (*domain.Player, string) {
	lang := langOf(r)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, lang.T("teammates.err.empty")
	}
	found, err := s.players.Search(r.Context(), query, teammateSearch)
	if err != nil {
		return nil, err.Error()
	}
	var byNick []*domain.Player
	for _, p := range found {
		if p.GameID != "" && strings.EqualFold(p.GameID, query) {
			return p, ""
		}
		if strings.EqualFold(p.Nickname, query) {
			byNick = append(byNick, p)
		}
	}
	switch len(byNick) {
	case 1:
		return byNick[0], ""
	case 0:
		return nil, lang.T("teammates.err.notfound", query)
	}
	return nil, lang.T("teammates.err.many", query)
}

func (s *Server) teammateAdd(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	other, msg := s.findTeammate(r, r.PostFormValue("partner"))
	if msg == "" && other.ID == player.ID {
		msg = langOf(r).T("teammates.err.self")
	}
	if msg != "" {
		s.renderPlayerCard(w, r, http.StatusUnprocessableEntity, player, msg, "", nil)
		return
	}

	note := strings.TrimSpace(r.PostFormValue("note"))
	if runes := []rune(note); len(runes) > domain.TeammateNoteMax {
		note = string(runes[:domain.TeammateNoteMax])
	}
	if err := s.teammates.Add(r.Context(), player.ID, other.ID, note, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "player.teammate.add", "player", player.ID,
		map[string]any{"nickname": player.Nickname, "with": other.Nickname, "with_id": other.ID, "note": note})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10)+"#teammates", http.StatusSeeOther)
}

func (s *Server) teammateRemove(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	other, err := strconv.ParseInt(r.PathValue("other"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return
	}
	if err := s.teammates.Remove(r.Context(), player.ID, other); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "player.teammate.remove", "player", player.ID,
		map[string]any{"nickname": player.Nickname, "with_id": other})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10)+"#teammates", http.StatusSeeOther)
}

// manualPairView — пара из состава партии, отмеченная руками.
type manualPairView struct {
	A, B coalitionSideView
	Note string
}

// teammatePairs — кто из игроков этой партии, как отметили админы, играет
// вместе. Сторона пары рисуется так же, как у пар из архива коалиций.
func (s *Server) teammatePairs(r *http.Request, state *supremacy.GameState, sides *gameSides) ([]manualPairView, error) {
	if s.teammates == nil || state == nil {
		return nil, nil
	}
	byUser := make(map[string]supremacy.Player, len(state.Players))
	ids := make([]string, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID == "" || p.IsAI {
			continue
		}
		byUser[strings.ToLower(p.SiteUserID)] = p
		ids = append(ids, p.SiteUserID)
	}
	pairs, err := s.teammates.Among(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]manualPairView, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, manualPairView{
			A:    s.coalitionSide(byUser[strings.ToLower(p.A)], sides),
			B:    s.coalitionSide(byUser[strings.ToLower(p.B)], sides),
			Note: p.Note,
		})
	}
	return out, nil
}
