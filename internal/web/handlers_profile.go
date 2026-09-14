package web

import (
	"errors"
	"net/http"
	"strings"

	"Vendetta_admin/internal/domain"
)

// profileForm — своя страница: человек рассказывает о себе сам. Чужой
// профиль отсюда не правится, пользователь берётся из сессии.
func (s *Server) profileForm(w http.ResponseWriter, r *http.Request) {
	s.renderProfile(w, r, http.StatusOK, currentUser(r), "", "")
}

// profileSave сохраняет имя, город и игровой ID.
func (s *Server) profileSave(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	fullName := strings.TrimSpace(r.PostFormValue("full_name"))
	city := strings.TrimSpace(r.PostFormValue("city"))
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))

	if err := domain.ValidateProfile(fullName, city, gameID); err != nil {
		// Введённое возвращаем в форму: перенабирать из-за одной ошибки обидно.
		draft := *me
		draft.FullName, draft.City, draft.GameID = fullName, city, gameID
		s.renderProfile(w, r, http.StatusBadRequest, &draft, errText(r, err), "")
		return
	}

	err := s.users.UpdateProfile(r.Context(), me.ID, fullName, city, gameID)
	// ID уникален: по нему регистрация находит аккаунт, и назвать своим
	// чужой ID значило бы закрыть его хозяину дорогу в админку.
	if errors.Is(err, domain.ErrGameIDTaken) {
		draft := *me
		draft.FullName, draft.City, draft.GameID = fullName, city, gameID
		s.renderProfile(w, r, http.StatusConflict, &draft, errText(r, err), "")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("профиль сохранён", "user_id", me.ID)

	saved := *me
	saved.FullName, saved.City, saved.GameID = fullName, city, gameID
	s.renderProfile(w, r, http.StatusOK, &saved, "", langOf(r).T("profile.saved"))
}

func (s *Server) renderProfile(w http.ResponseWriter, r *http.Request, status int,
	user *domain.User, errMsg, notice string) {

	s.render(w, r, status, "profile", map[string]any{
		"Profile": user, "Error": errMsg, "Notice": notice,
	})
}
