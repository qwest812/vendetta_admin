package web

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"Vendetta_admin/internal/auth"
	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

type Server struct {
	log      *slog.Logger
	auth     *auth.Service
	users    *repo.Users
	sessions *repo.Sessions
	audit    *repo.Audit
	players  *repo.Players
	traits   *repo.Traits
	health   func(context.Context) error
	pages    pages
}

type Deps struct {
	Log      *slog.Logger
	Auth     *auth.Service
	Users    *repo.Users
	Sessions *repo.Sessions
	Audit    *repo.Audit
	Players  *repo.Players
	Traits   *repo.Traits
	// Health проверяет живость зависимостей для /healthz.
	Health func(context.Context) error
}

func NewServer(d Deps) (*Server, error) {
	tmpls, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{
		log: d.Log, auth: d.Auth, users: d.Users, sessions: d.Sessions,
		audit: d.Audit, players: d.Players, traits: d.Traits,
		health: d.Health, pages: tmpls,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	// Публичное: проверка живости и вход.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.Handle("POST /login", http.HandlerFunc(s.loginSubmit))
	mux.Handle("POST /logout", auth.VerifyCSRF(http.HandlerFunc(s.logout)))

	// Поиск и просмотр — всем авторизованным.
	user := auth.RequireRole(domain.RoleUser)
	mux.Handle("GET /{$}", user(http.HandlerFunc(s.home)))
	mux.Handle("GET /search", user(http.HandlerFunc(s.search)))
	mux.Handle("GET /players/{id}", user(http.HandlerFunc(s.playerCard)))

	// Заметки пишут все авторизованные: карточку наполняют те, кто работает
	// с игроками, а не только админы. Удаление разрешает сам хендлер —
	// свою заметку убирает автор, чужую админ и выше.
	mux.Handle("POST /players/{id}/notes", user(auth.VerifyCSRF(http.HandlerFunc(s.noteCreate))))
	mux.Handle("POST /notes/{noteID}/delete", user(auth.VerifyCSRF(http.HandlerFunc(s.noteDelete))))

	// Управление доступами: админ и выше.
	admin := auth.RequireRole(domain.RoleAdmin)
	mux.Handle("GET /users", admin(http.HandlerFunc(s.usersList)))
	mux.Handle("POST /users", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersCreate))))
	mux.Handle("POST /users/{id}/role", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersSetRole))))
	mux.Handle("POST /users/{id}/active", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersSetActive))))
	mux.Handle("POST /users/{id}/password", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersResetPassword))))
	mux.Handle("GET /audit", admin(http.HandlerFunc(s.auditList)))

	// Карточки игроков и справочник признаков — админ и выше.
	mux.Handle("GET /players/new", admin(http.HandlerFunc(s.playerNew)))
	mux.Handle("POST /players", admin(auth.VerifyCSRF(http.HandlerFunc(s.playerCreate))))
	mux.Handle("GET /players/{id}/edit", admin(http.HandlerFunc(s.playerEdit)))
	mux.Handle("POST /players/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.playerUpdate))))

	mux.Handle("GET /traits", admin(http.HandlerFunc(s.traitsList)))
	mux.Handle("POST /traits", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitCreate))))
	mux.Handle("POST /traits/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitUpdate))))
	mux.Handle("POST /traits/{id}/delete", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitDelete))))

	// Удаление пользователей и карточек — только рут.
	root := auth.RequireRole(domain.RoleRoot)
	mux.Handle("POST /users/{id}/delete", root(auth.VerifyCSRF(http.HandlerFunc(s.usersDelete))))
	mux.Handle("POST /players/{id}/delete", root(auth.VerifyCSRF(http.HandlerFunc(s.playerDelete))))

	return s.recoverPanic(s.logRequests(s.auth.Attach(mux)))
}

// healthz отвечает 200, только если база отвечает: по нему docker
// определяет готовность контейнера.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if s.health != nil {
		if err := s.health(ctx); err != nil {
			s.log.Error("проверка живости не прошла", "err", err)
			http.Error(w, "база недоступна", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func currentUser(r *http.Request) *domain.User { return auth.UserFrom(r.Context()) }

func currentSession(r *http.Request) *repo.Session { return auth.SessionFrom(r.Context()) }

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("ошибка обработки запроса", "err", err, "method", r.Method, "path", r.URL.Path)
	http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("запрос", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				w.Header().Set("Connection", "close")
				s.log.Error("паника", "recover", rec, "path", r.URL.Path)
				http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
