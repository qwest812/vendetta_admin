package auth

import (
	"context"
	"crypto/subtle"
	"net/http"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

type ctxKey int

const sessionKey ctxKey = iota

// Attach кладёт сессию в контекст, если она есть. Не требует авторизации:
// публичные страницы тоже должны знать, кто их смотрит.
func (s *Service) Attach(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, err := s.Current(r.Context(), r); err == nil {
			r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole пропускает дальше только авторизованных с ролью не ниже min.
func RequireRole(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := SessionFrom(r.Context())
			if sess == nil {
				redirectToLogin(w, r)
				return
			}
			if !sess.User.Role.AtLeast(min) {
				http.Error(w, "Недостаточно прав", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// VerifyCSRF защищает изменяющие запросы. Проверяем токен из формы против
// токена сессии — cookie SameSite=Lax сам по себе не покрывает все случаи.
func VerifyCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess := SessionFrom(r.Context())
		if sess == nil {
			redirectToLogin(w, r)
			return
		}
		// Разбираем форму всегда: обработчики ниже рассчитывают на r.PostForm.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Некорректная форма", http.StatusBadRequest)
			return
		}
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.PostFormValue("csrf_token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
			http.Error(w, "Сессия устарела, обновите страницу", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SessionFrom(ctx context.Context) *repo.Session {
	sess, _ := ctx.Value(sessionKey).(*repo.Session)
	return sess
}

func UserFrom(ctx context.Context) *domain.User {
	if sess := SessionFrom(ctx); sess != nil {
		return sess.User
	}
	return nil
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
