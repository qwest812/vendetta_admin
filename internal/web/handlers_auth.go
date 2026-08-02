package web

import (
	"errors"
	"net/http"
	"strings"

	"Vendetta_admin/internal/domain"
)

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", map[string]any{"Error": "", "Email": ""})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная форма", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")

	user, err := s.auth.Login(r.Context(), w, email, password)
	if errors.Is(err, domain.ErrInvalidLogin) {
		s.log.Warn("неудачный вход", "email", email, "ip", r.RemoteAddr)
		s.render(w, r, http.StatusUnauthorized, "login", map[string]any{
			"Error": "Неверная почта или пароль",
			"Email": email,
		})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.log.Info("вход выполнен", "user_id", user.ID, "email", user.Email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), w, r); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
