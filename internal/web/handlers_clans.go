package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

// clanMembersLimit ограничивает состав на карточке клана. Кланы бывают
// большими, а страница должна оставаться страницей.
const clanMembersLimit = 200

func (s *Server) clansList(w http.ResponseWriter, r *http.Request) {
	s.renderClans(w, r, http.StatusOK, "")
}

func (s *Server) renderClans(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	clans, err := s.clans.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "clans", map[string]any{
		"Clans": clans, "Statuses": domain.ClanStatuses, "Error": errMsg,
	})
}

// clanCard показывает клан и его состав.
func (s *Server) clanCard(w http.ResponseWriter, r *http.Request) {
	clan, ok := s.loadClan(w, r)
	if !ok {
		return
	}
	players, err := s.players.ByClan(r.Context(), clan.ID, clanMembersLimit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.scorePlayers(r, players); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "clan", map[string]any{
		"Clan": clan, "Statuses": domain.ClanStatuses,
		"Players": players, "Limit": clanMembersLimit,
	})
}

// clanCreate заводит клан заранее — чтобы пометить альянс до того, как в базе
// появится хоть одна его карточка. Обычные кланы заводятся сами, из формы
// игрока.
func (s *Server) clanCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	status, ok := domain.ParseClanStatus(r.PostFormValue("status"))
	if !ok {
		status = domain.ClanNeutral
	}
	if err := validateClanName(name); err != nil {
		s.renderClans(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	clan, err := s.clans.Create(r.Context(), name, status)
	if errors.Is(err, domain.ErrClanTaken) {
		s.renderClans(w, r, http.StatusUnprocessableEntity, "Клан с таким названием уже есть")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditOn(r, "clan.create", "clan", clan.ID,
		map[string]any{"name": name, "status": string(status)})
	http.Redirect(w, r, "/clans", http.StatusSeeOther)
}

func (s *Server) clanUpdate(w http.ResponseWriter, r *http.Request) {
	clan, ok := s.loadClan(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	status, ok := domain.ParseClanStatus(r.PostFormValue("status"))
	if !ok {
		status = clan.Status
	}
	if err := validateClanName(name); err != nil {
		s.renderClans(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	err := s.clans.Update(r.Context(), clan.ID, name, status)
	if errors.Is(err, domain.ErrClanTaken) {
		s.renderClans(w, r, http.StatusUnprocessableEntity, "Клан с таким названием уже есть")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Статус — политика, поэтому в журнале остаётся и прежнее значение.
	s.logAuditOn(r, "clan.update", "clan", clan.ID, map[string]any{
		"name": name, "was": clan.Name, "players": clan.Players,
		"status_from": string(clan.Status), "status_to": string(status),
	})
	http.Redirect(w, r, redirectBack(r, "/clans"), http.StatusSeeOther)
}

// clanDelete убирает клан. Карточки игроков остаются — они просто теряют клан.
func (s *Server) clanDelete(w http.ResponseWriter, r *http.Request) {
	clan, ok := s.loadClan(w, r)
	if !ok {
		return
	}
	if err := s.clans.Delete(r.Context(), clan.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "clan.delete", "clan", clan.ID,
		map[string]any{"name": clan.Name, "status": string(clan.Status), "players": clan.Players})
	http.Redirect(w, r, "/clans", http.StatusSeeOther)
}

func (s *Server) loadClan(w http.ResponseWriter, r *http.Request) (domain.Clan, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return domain.Clan{}, false
	}
	clan, err := s.clans.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Клан не найден", http.StatusNotFound)
		return domain.Clan{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return domain.Clan{}, false
	}
	return clan, true
}

// redirectBack возвращает туда, откуда пришла форма: статус правится и в
// списке, и на карточке клана, и уходить со страницы после этого незачем.
// Адрес берётся из скрытого поля, а не из Referer, и проверяется на свой.
func redirectBack(r *http.Request, fallback string) string {
	back := r.PostFormValue("back")
	if strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") {
		return back
	}
	return fallback
}

func validateClanName(name string) error {
	switch n := len([]rune(name)); {
	case n == 0:
		return errors.New("Укажите название клана")
	case n > 64:
		return errors.New("Название длиннее 64 символов")
	}
	return nil
}
