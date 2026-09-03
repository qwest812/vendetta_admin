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
	clans    *repo.Clans
	traits   *repo.Traits
	// Личные списки: устроены одинаково, различаются таблицей и словами.
	enemies *relationSection
	friends *relationSection
	games   gameSource
	// alliances — кеш кланов Supremacy: сайт игры отвечает про одного
	// игрока за раз, а карту красят все сразу.
	alliances *repo.Alliances
	// gamePlayers — что игра рассказала про игроков при заходе в партию:
	// ник и бан. Пишется на каждом заходе, читается в карточке игрока.
	gamePlayers *repo.GamePlayers
	tasks       *repo.GameTasks
	// heroEvery — как часто воркер жмёт кнопку Мейв; показывается на
	// странице партии, чтобы обещание в интерфейсе не расходилось с делом.
	heroEvery time.Duration
	health    func(context.Context) error
	pages     pages
}

type Deps struct {
	Log      *slog.Logger
	Auth     *auth.Service
	Users    *repo.Users
	Sessions *repo.Sessions
	Audit    *repo.Audit
	Players  *repo.Players
	Clans    *repo.Clans
	Traits   *repo.Traits
	Enemies  *repo.Relations
	Friends  *repo.Relations
	// Games — аккаунт Supremacy 1914 для рутового раздела «Игры».
	// Пусто, если аккаунт не настроен: раздел тогда скажет об этом сам.
	Games gameSource
	// Alliances — кеш кланов из самой Supremacy, по ним красится карта партии.
	Alliances *repo.Alliances
	// GamePlayers — ники и баны игроков, увиденные при заходах в партии.
	GamePlayers *repo.GamePlayers
	// Tasks — что админка делает в партиях сама, и HeroEvery — как часто.
	Tasks     *repo.GameTasks
	HeroEvery time.Duration
	// Health проверяет живость зависимостей для /healthz.
	Health func(context.Context) error
}

func NewServer(d Deps) (*Server, error) {
	tmpls, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		log: d.Log, auth: d.Auth, users: d.Users, sessions: d.Sessions,
		audit: d.Audit, players: d.Players, clans: d.Clans, traits: d.Traits,
		games: d.Games, alliances: d.Alliances, gamePlayers: d.GamePlayers,
		tasks: d.Tasks, heroEvery: d.HeroEvery,
		health: d.Health, pages: tmpls,
	}
	s.enemies = newRelationSection(s, d.Enemies, enemyWords)
	s.friends = newRelationSection(s, d.Friends, friendWords)
	return s, nil
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
	// Кланы смотрят все: это такой же справочник, как карточки игроков.
	// Статусы правит админ — они политика, а не наблюдение.
	mux.Handle("GET /clans", user(http.HandlerFunc(s.clansList)))
	mux.Handle("GET /clans/{id}", user(http.HandlerFunc(s.clanCard)))
	mux.Handle("GET /faq", user(http.HandlerFunc(s.faq)))

	// Свой профиль ведёт каждый сам: имя, город и игровой ник — это справка
	// для своих. Чужой профиль отсюда не правится, пользователь берётся
	// из сессии.
	mux.Handle("GET /profile", user(http.HandlerFunc(s.profileForm)))
	mux.Handle("POST /profile", user(auth.VerifyCSRF(http.HandlerFunc(s.profileSave))))

	// Личные списки — враги и друзья — ведёт каждый сам: и обычный
	// пользователь тоже, поэтому права те же, что у заметок. Чужой список
	// не показывается и не правится — пользователь берётся из сессии,
	// а не из адреса. Разделы устроены одинаково, поэтому и роуты общие.
	for _, sec := range []*relationSection{s.enemies, s.friends} {
		path := "/" + sec.words.Path
		mux.Handle("GET "+path, user(http.HandlerFunc(sec.list)))
		mux.Handle("GET "+path+"/search", user(http.HandlerFunc(sec.search)))
		mux.Handle("POST "+path, user(auth.VerifyCSRF(http.HandlerFunc(sec.add))))
		mux.Handle("POST "+path+"/{playerID}", user(auth.VerifyCSRF(http.HandlerFunc(sec.update))))
		mux.Handle("POST "+path+"/{playerID}/mark", user(auth.VerifyCSRF(http.HandlerFunc(sec.mark))))
		mux.Handle("POST "+path+"/{playerID}/delete", user(auth.VerifyCSRF(http.HandlerFunc(sec.remove))))
	}

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

	mux.Handle("POST /clans", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanCreate))))
	mux.Handle("POST /clans/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanUpdate))))
	mux.Handle("POST /clans/{id}/delete", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanDelete))))

	mux.Handle("GET /traits", admin(http.HandlerFunc(s.traitsList)))
	mux.Handle("POST /traits", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitCreate))))
	mux.Handle("POST /traits/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitUpdate))))
	mux.Handle("POST /traits/{id}/delete", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitDelete))))

	root := auth.RequireRole(domain.RoleRoot)

	// Смотреть партии может тот, кому рут выдал доступ: право персональное,
	// в лестницу ролей не встроено. Заход в состояние партии игра засчитывает
	// как вход в неё, поэтому раздаётся он поштучно, а не всем подряд.
	games := func(h http.HandlerFunc) http.Handler { return user(auth.RequireGamesAccess(h)) }
	mux.Handle("GET /games", games(s.gamesList))
	// Проверка партии по номеру: сведения о ней сайт отдаёт про любую,
	// заходом в партию это не считается. Роут стоит раньше /games/{id}
	// по правилам мультиплексора — он точнее шаблона с параметром.
	mux.Handle("GET /games/check", games(s.gamesCheck))
	mux.Handle("GET /games/{id}", games(s.gameCard))

	// Только рут: он один жмёт кнопки в чужой партии от имени общего
	// аккаунта, выдаёт доступ к разделу и удаляет пользователей и карточки.
	mux.Handle("POST /games/{id}/hero", root(auth.VerifyCSRF(http.HandlerFunc(s.gameHeroToggle))))
	mux.Handle("POST /games/{id}/hero/run", root(auth.VerifyCSRF(http.HandlerFunc(s.gameHeroRun))))
	mux.Handle("POST /users/{id}/games", root(auth.VerifyCSRF(http.HandlerFunc(s.usersSetGamesAccess))))
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
