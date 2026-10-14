package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
)

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", map[string]any{"Error": "", "Login": ""})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, langOf(r).T("err.badform"), http.StatusBadRequest)
		return
	}
	// В одно поле вводят либо почту, либо ник — что удобнее.
	login := strings.TrimSpace(r.PostFormValue("login"))
	password := r.PostFormValue("password")

	subnet := loginSubnet(r)
	if s.loginLimits.blocked(subnet, login, time.Now()) {
		s.log.Warn("вход: предел неудачных попыток", "login", login, "subnet", subnet)
		s.render(w, r, http.StatusTooManyRequests, "login", map[string]any{
			"Error": langOf(r).T("err.login.toomany"),
			"Login": login,
		})
		return
	}

	user, err := s.auth.Login(r.Context(), w, r, login, password)
	if errors.Is(err, domain.ErrInvalidLogin) {
		s.loginLimits.failed(subnet, login, time.Now())
		s.log.Warn("неудачный вход", "login", login, "ip", r.RemoteAddr)
		s.render(w, r, http.StatusUnauthorized, "login", map[string]any{
			"Error": langOf(r).T("err.badlogin"),
			"Login": login,
		})
		return
	}
	if errors.Is(err, domain.ErrLoginBlocked) {
		s.log.Warn("вход заблокированного", "login", login, "ip", r.RemoteAddr)
		s.render(w, r, http.StatusForbidden, "login", map[string]any{
			"Error": langOf(r).T("err.login.blocked"),
			"Login": login,
		})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.log.Info("вход выполнен", "user_id", user.ID, "nickname", user.Nickname)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), w, r); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// loginSubnet — подсеть, по которой считаются неудачные входы. Адрес,
// который не разобрался, считается сам по себе: отказывать из-за этого
// во входе незачем, а пропускать мимо счёта нельзя.
func loginSubnet(r *http.Request) string {
	place, err := domain.ParseLoginPlace(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return place.Subnet
}
