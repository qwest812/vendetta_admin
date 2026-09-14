package web

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"Vendetta_admin/internal/auth"
	"Vendetta_admin/internal/domain"
)

// API для расширения Chrome. Живёт отдельно от страниц сайта и устроено
// по-другому: вход по токену в заголовке Authorization, а не по куке, и
// ответы — JSON, а не разметка.
//
// Проверка происхождения запроса (crossOrigin) и CSRF здесь не нужны
// и не стоят: обе защищают куку, которую браузер подкладывает к запросу
// сам. Токен в заголовок сам не подкладывается, а чужая страница поставить
// его не может — для этого браузер спросил бы разрешения у сервера, а
// разрешать чужим мы ничего не отвечаем. Расширению это разрешение не
// нужно: доступ к адресу сервера оно получает от человека при установке.

// apiBodyLimit — больше этого вход не присылает: почта и пароль.
const apiBodyLimit = 16 << 10

// apiUser — человек, как его видит расширение. Только то, что нужно
// показать в окне: хеш пароля, пакет и прочее внутреннее сюда не идут.
type apiUser struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

func toAPIUser(u *domain.User) apiUser {
	return apiUser{ID: u.ID, Nickname: u.Nickname, Email: u.Email, Role: string(u.Role)}
}

// apiHandler — все адреса /api/. Сессия берётся из заголовка, куки
// не читаются вовсе.
func (s *Server) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", s.apiLogin)
	mux.Handle("GET /api/auth/me", apiRequireUser(http.HandlerFunc(s.apiMe)))
	mux.HandleFunc("POST /api/auth/logout", s.apiLogout)
	// Незнакомый адрес под /api/ — JSON, а не страница 404 сайта:
	// расширение разбирает ответы как данные.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "нет такого адреса")
	})
	return s.auth.AttachExtension(mux)
}

// apiRequireUser пускает дальше только с живым токеном.
func apiRequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			writeAPIError(w, http.StatusUnauthorized, langOf(r).T("api.unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiLogin — вход расширения по почте и паролю. Отказ один на всё: нет
// такой почты, не тот пароль, закрыт доступ — снаружи неразличимы, как
// и на форме сайта.
func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || req.Password == "" {
		writeAPIError(w, http.StatusBadRequest, lang.T("api.login.empty"))
		return
	}

	user, token, expires, err := s.auth.LoginExtension(r.Context(), r, email, req.Password)
	if errors.Is(err, domain.ErrInvalidLogin) {
		s.log.Warn("неудачный вход из расширения", "login", email, "ip", r.RemoteAddr)
		writeAPIError(w, http.StatusUnauthorized, lang.T("err.badlogin"))
		return
	}
	if err != nil {
		s.log.Error("вход из расширения", "err", err)
		writeAPIError(w, http.StatusInternalServerError, lang.T("api.internal"))
		return
	}

	s.log.Info("вход из расширения", "user_id", user.ID, "nickname", user.Nickname)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "expires_at": expires.UTC().Format(time.RFC3339), "user": toAPIUser(user),
	})
}

// apiMe — кто владелец токена. Расширение спрашивает это при открытии:
// так оно узнаёт, что токен ещё жив, и показывает, под кем вошло.
func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": toAPIUser(currentUser(r))})
}

// apiLogout гасит токен. Отвечает «готово» и тогда, когда гасить было
// нечего: расширению важен итог — оно больше не вошло.
func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.LogoutExtension(r.Context(), r); err != nil {
		s.log.Error("выход из расширения", "err", err)
		writeAPIError(w, http.StatusInternalServerError, langOf(r).T("api.internal"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readJSON разбирает тело запроса. Принимается только application/json:
// простую форму чужая страница отправить может, а JSON — только спросив
// разрешения у сервера, которого мы не даём. Так вход нельзя дёрнуть
// с чужого сайта даже вслепую.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, langOf(r).T("api.json"))
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, apiBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, langOf(r).T("api.badjson"))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Ответы с токеном и данными людей не должны оседать ни в каком кэше.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError — ошибка в виде {"error": "..."}: текст готов к показу
// в окне расширения.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
