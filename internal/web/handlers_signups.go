package web

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"Vendetta_admin/internal/domain"
)

// signupsPage — сколько людей зарегистрировалось само и как это число
// меняется от отрезка к отрезку. Рутовая страница, как и журнал входов.
func (s *Server) signupsPage(w http.ResponseWriter, r *http.Request) {
	// Непонятный отрезок в адресе — отрезок по умолчанию, а не отказ:
	// строку запроса правят и руками.
	days := domain.SignupDefaultDays
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && slices.Contains(domain.SignupPeriods, n) {
		days = n
	}

	at, err := s.users.SignupTimes(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "signups", map[string]any{
		"Stats":   domain.CountSignups(at, time.Now(), days, time.Local),
		"Periods": domain.SignupPeriods,
		"Days":    days,
	})
}
