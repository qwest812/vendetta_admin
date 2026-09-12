package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

// Запреты браузеру одинаковы для всех ответов и не зависят от того,
// кто пришёл: проверяем на обычной странице и на ошибке.
func TestSecurityHeaders(t *testing.T) {
	want := map[string]string{
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
	}
	inner := map[string]http.Handler{
		"обычный ответ": okHandler(),
		"ошибка": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "нет", http.StatusForbidden)
		}),
	}
	for name, h := range inner {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			securityHeaders(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			for k, v := range want {
				if got := w.Header().Get(k); got != v {
					t.Errorf("%s = %q, ожидался %q", k, got, v)
				}
			}
		})
	}
}

// Чужая страница не должна дёргать изменяющие роуты, но читать и ходить
// по ссылкам браузер обязан уметь: GET безопасен и проходит всегда.
func TestCrossOrigin(t *testing.T) {
	s := &Server{log: slog.New(slog.DiscardHandler)}
	h := s.crossOrigin(okHandler())

	tests := []struct {
		name   string
		method string
		site   string
		origin string
		host   string
		want   int
	}{
		{"своя форма", http.MethodPost, "same-origin", "https://admin.example", "admin.example", http.StatusOK},
		{"чужая форма", http.MethodPost, "cross-site", "https://evil.example", "admin.example", http.StatusForbidden},
		{"чужой переход по ссылке", http.MethodGet, "cross-site", "https://evil.example", "admin.example", http.StatusOK},
		{"старый браузер, свой хост", http.MethodPost, "", "https://admin.example", "admin.example", http.StatusOK},
		{"старый браузер, чужой хост", http.MethodPost, "", "https://evil.example", "admin.example", http.StatusForbidden},
		{"не браузер: заголовков нет", http.MethodPost, "", "", "admin.example", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/players/1/delete", nil)
			r.Host = tt.host
			if tt.site != "" {
				r.Header.Set("Sec-Fetch-Site", tt.site)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Errorf("код = %d, ожидался %d", w.Code, tt.want)
			}
		})
	}
}
