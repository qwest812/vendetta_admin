package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/auth"
)

// apiTestServer — сервер без базы: проверяемые здесь пути до неё не доходят.
// Нет токена — сессию не ищут; не JSON — вход не проверяют.
func apiTestServer(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	s, err := NewServer(Deps{Log: log, Auth: auth.NewService(nil, nil, nil, log, time.Hour, false)})
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	return s.Handler()
}

// API расширения живёт по токену в заголовке, а не по куке, поэтому
// проверка происхождения ему не нужна и стоять не должна: запросы
// расширения браузер помечает как пришедшие не с нашего сайта. Формы
// сайта при этом по-прежнему закрыты от чужих страниц.
func TestAPIBypassesCrossOriginButSiteDoesNot(t *testing.T) {
	h := apiTestServer(t)

	cases := []struct {
		name string
		path string
		want int
	}{
		{"выход расширения с чужого адреса", "/api/auth/logout", http.StatusNoContent},
		{"форма сайта с чужого адреса", "/logout", http.StatusForbidden},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, c.path, nil)
		r.Host = "admin.example"
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "chrome-extension://abcdefghijklmnop")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: код %d, ожидался %d", c.name, w.Code, c.want)
		}
	}
}

// Ответы API — JSON с текстом ошибки, готовым к показу в окне расширения,
// а не страницы сайта с редиректом на форму входа.
func TestAPIErrorsAreJSON(t *testing.T) {
	h := apiTestServer(t)

	cases := []struct {
		name   string
		method string
		path   string
		ctype  string
		body   string
		cookie bool
		want   int
	}{
		{name: "кто я без токена", method: http.MethodGet, path: "/api/auth/me", want: http.StatusUnauthorized},
		// Кука сайта токеном не считается: API куки не читает вовсе.
		{name: "кто я с кукой сайта", method: http.MethodGet, path: "/api/auth/me", cookie: true, want: http.StatusUnauthorized},
		// Простую форму чужая страница отправить может, JSON — нет.
		{name: "вход формой", method: http.MethodPost, path: "/api/auth/login",
			ctype: "application/x-www-form-urlencoded", body: "email=a@b.c&password=x", want: http.StatusUnsupportedMediaType},
		{name: "вход без пароля", method: http.MethodPost, path: "/api/auth/login",
			ctype: "application/json", body: `{"email":"a@b.c"}`, want: http.StatusBadRequest},
		{name: "вход с лишним полем", method: http.MethodPost, path: "/api/auth/login",
			ctype: "application/json", body: `{"email":"a@b.c","password":"x","role":"root"}`, want: http.StatusBadRequest},
		{name: "незнакомый адрес", method: http.MethodGet, path: "/api/nope", want: http.StatusNotFound},
		// Не тем методом — тоже «нет такого адреса» в JSON: общий
		// обработчик /api/ перехватывает раньше, чем мультиплексор скажет 405.
		{name: "вход не тем методом", method: http.MethodGet, path: "/api/auth/login", want: http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			if c.ctype != "" {
				r.Header.Set("Content-Type", c.ctype)
			}
			if c.cookie {
				r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "browser-session"})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("код %d, ожидался %d: %s", w.Code, c.want, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, ожидался JSON", ct)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Errorf("нет текста ошибки: %s", w.Body.String())
			}
		})
	}
}

// Номера из запроса силы уходят и в базу, и к сайту игры, поэтому
// пропускаются только цифры; свой номер идёт в тот же список один раз.
func TestPowerIDs(t *testing.T) {
	ids, ok := powerIDs("101408369", []string{"75396888", "101408369", "75396888", "123"})
	if !ok || strings.Join(ids, ",") != "101408369,75396888,123" {
		t.Errorf("список = %v, ok = %v", ids, ok)
	}

	many := make([]string, apiPowerMax+1)
	for i := range many {
		many[i] = "1"
	}
	bad := []struct {
		name    string
		me      string
		players []string
	}{
		{"без своего номера", "", []string{"1"}},
		{"свой номер не цифры", "abc", []string{"1"}},
		{"пустой состав", "1", nil},
		{"чужой номер не цифры", "1", []string{"2", "1 OR 1=1"}},
		{"слишком много игроков", "1", many},
	}
	for _, c := range bad {
		if _, ok := powerIDs(c.me, c.players); ok {
			t.Errorf("%s: запрос пропущен", c.name)
		}
	}
}
