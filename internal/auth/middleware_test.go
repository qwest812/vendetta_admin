package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// withUser подкладывает сессию так же, как это делает Attach.
func withUser(u *domain.User) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/games", nil)
	if u == nil {
		return r
	}
	sess := &repo.Session{User: u, CSRFToken: "t"}
	return r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

// Доступ к играм персональный: роль его не даёт и не отменяет, кроме рута —
// тот ходит в раздел всегда.
func TestRequireGamesAccess(t *testing.T) {
	tests := []struct {
		name string
		user *domain.User
		want int
	}{
		{"рут без флага", &domain.User{Role: domain.RoleRoot}, http.StatusOK},
		{"админ без флага", &domain.User{Role: domain.RoleAdmin}, http.StatusForbidden},
		{"пользователь без флага", &domain.User{Role: domain.RoleUser}, http.StatusForbidden},
		{"пользователь с флагом", &domain.User{Role: domain.RoleUser, GamesAccess: true}, http.StatusOK},
		{"админ с флагом", &domain.User{Role: domain.RoleAdmin, GamesAccess: true}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			RequireGamesAccess(okHandler()).ServeHTTP(w, withUser(tt.user))
			if w.Code != tt.want {
				t.Errorf("код = %d, ожидался %d", w.Code, tt.want)
			}
		})
	}
}

// Неавторизованного разворачивает проверка роли, но и в одиночку проверка
// доступа его дальше не пускает.
func TestRequireGamesAccessAnonymous(t *testing.T) {
	w := httptest.NewRecorder()
	RequireGamesAccess(okHandler()).ServeHTTP(w, withUser(nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("код = %d, ожидался 403", w.Code)
	}
}

// Кнопки в партии жмёт только рут: доступ к разделу их не открывает.
func TestRequireRootForHeroActions(t *testing.T) {
	tests := []struct {
		name string
		user *domain.User
		want int
	}{
		{"рут", &domain.User{Role: domain.RoleRoot}, http.StatusOK},
		{"админ", &domain.User{Role: domain.RoleAdmin}, http.StatusForbidden},
		{"пользователь с доступом к играм", &domain.User{Role: domain.RoleUser, GamesAccess: true}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			RequireRole(domain.RoleRoot)(okHandler()).ServeHTTP(w, withUser(tt.user))
			if w.Code != tt.want {
				t.Errorf("код = %d, ожидался %d", w.Code, tt.want)
			}
		})
	}
}
