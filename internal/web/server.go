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
	// feedback — обращения в админку и переписка по ним.
	feedback *repo.Feedback
	// alliances — кеш кланов Supremacy: сайт игры отвечает про одного
	// игрока за раз, а карту красят все сразу.
	alliances *repo.Alliances
	// topAlliances — снимок верхушки рейтинга кланов игры. Отдельно
	// от alliances: там клан игрока, здесь место клана в рейтинге.
	topAlliances *repo.TopAlliances
	// userStats — боевой счёт игроков с сайта игры: уровень и кд. По ним
	// карта сравнивает участников партии со смотрящим.
	userStats *repo.UserStats
	// gamePlayers — что игра рассказала про игроков при заходе в партию:
	// ник и бан. Пишется на каждом заходе, читается в карточке игрока.
	gamePlayers *repo.GamePlayers
	// coalitions — архив «кто с кем союзничал»: очередь обхода и то,
	// что в ней нашлось. settings — общие переключатели, из них раздел
	// берёт один: включён ли обход.
	coalitions *repo.Coalitions
	settings   *repo.Settings
	// heroes — справочник героев игры: снимок и портреты. Пусто в базе
	// означает «живём вшитым в образ», см. heroSnapshot.
	heroes *repo.Heroes
	tasks  *repo.GameTasks
	// heroEveryMax — верхняя граница той же паузы: она случайна, и на
	// странице партии написан диапазон, а не одно число.
	heroEveryMax time.Duration
	// heroEvery — нижняя граница паузы воркера с кнопкой Мейв; показывается на
	// странице партии, чтобы обещание в интерфейсе не расходилось с делом.
	heroEvery time.Duration
	// checks — дневной счёт проверок карты: сколько их у кого осталось.
	checks *repo.MapChecks
	// checked — личные списки проверенных партий: у каждого свой.
	checked *repo.CheckedGames
	health  func(context.Context) error
	// cookieSecure — тот же флаг, что у куки сессии: язык хранится в куке,
	// и жить она должна по тем же правилам.
	cookieSecure bool
	pages        site
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
	// Feedback — обратная связь: обращения пользователей и ответы на них.
	Feedback *repo.Feedback
	// Games — аккаунт Supremacy 1914 для рутового раздела «Игры».
	// Пусто, если аккаунт не настроен: раздел тогда скажет об этом сам.
	Games gameSource
	// Alliances — кеш кланов из самой Supremacy, по ним красится карта партии.
	Alliances *repo.Alliances
	// TopAlliances — верхушка рейтинга кланов: по ней карта отмечает,
	// кто в партии играет за топовый клан.
	TopAlliances *repo.TopAlliances
	// GamePlayers — ники и баны игроков, увиденные при заходах в партии.
	GamePlayers *repo.GamePlayers
	// UserStats — боевой счёт игроков: уровень и кд для сравнения на карте.
	UserStats *repo.UserStats
	// Tasks — что админка делает в партиях сама, HeroEvery и HeroEveryMax —
	// границы случайной паузы между заходами.
	Tasks        *repo.GameTasks
	HeroEvery    time.Duration
	HeroEveryMax time.Duration
	// Coalitions — архив коалиций, Settings — общие переключатели админки.
	Coalitions *repo.Coalitions
	Settings   *repo.Settings
	// Heroes — справочник героев Supremacy, каким его обновляет рут.
	Heroes *repo.Heroes
	// Checked — какие партии кто смотрел: список у каждого свой.
	Checked *repo.CheckedGames
	// Checks — дневной счёт проверок карты.
	Checks *repo.MapChecks
	// Health проверяет живость зависимостей для /healthz.
	Health func(context.Context) error
	// CookieSecure — ставить ли кукам флаг Secure. Тот же, что у сессии.
	CookieSecure bool
}

func NewServer(d Deps) (*Server, error) {
	tmpls, err := parseSite()
	if err != nil {
		return nil, err
	}
	s := &Server{
		log: d.Log, auth: d.Auth, users: d.Users, sessions: d.Sessions,
		audit: d.Audit, players: d.Players, clans: d.Clans, traits: d.Traits,
		feedback: d.Feedback,
		games:    d.Games, alliances: d.Alliances, topAlliances: d.TopAlliances,
		gamePlayers: d.GamePlayers, userStats: d.UserStats,
		coalitions: d.Coalitions, settings: d.Settings, heroes: d.Heroes,
		checked: d.Checked, checks: d.Checks,
		tasks: d.Tasks, heroEvery: d.HeroEvery, heroEveryMax: d.HeroEveryMax,
		health: d.Health, cookieSecure: d.CookieSecure, pages: tmpls,
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

	// Публичное: проверка живости, вход и выбор языка. Язык публичен
	// намеренно: переключатель нужен и на странице входа.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /lang/{lang}", s.setLang)
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
	// Портреты героев: часть из них лежит в базе, часть вшита в образ,
	// поэтому отдаёт их обработчик, а не файловый сервер статики.
	mux.Handle("GET /heroes/img/{name}", user(http.HandlerFunc(s.heroImage)))

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

	// Обратная связь: писать может каждый, кому выдан доступ. Своё обращение
	// видит автор, все — админ; отвечают обе стороны, а закрывает админ или
	// сам автор. Кто есть кто, решает обработчик: пользователь берётся
	// из сессии, а не из адреса.
	mux.Handle("GET /feedback", user(http.HandlerFunc(s.feedbackList)))
	mux.Handle("POST /feedback", user(auth.VerifyCSRF(http.HandlerFunc(s.feedbackCreate))))
	mux.Handle("GET /feedback/{id}", user(http.HandlerFunc(s.feedbackTicket)))
	mux.Handle("POST /feedback/{id}/reply", user(auth.VerifyCSRF(http.HandlerFunc(s.feedbackReply))))
	mux.Handle("POST /feedback/{id}/close", user(auth.VerifyCSRF(http.HandlerFunc(s.feedbackClose))))

	// Признаки и комментарии оставляют все авторизованные: замечает
	// поведение тот, кто играет рядом, а не тот, у кого есть право править
	// карточку. Отметки идут в журнал так же, как правки карточки, и обе
	// формы личные — правят они только своё.
	//
	// Удаление комментария разрешает сам хендлер: свой убирает автор, чужой
	// админ и выше. Снятие признака у всех разом — админское, это средство
	// против наговора.
	mux.Handle("POST /players/{id}/traits", user(auth.VerifyCSRF(http.HandlerFunc(s.traitsSave))))
	mux.Handle("POST /players/{id}/heroes", user(auth.VerifyCSRF(http.HandlerFunc(s.heroesSave))))
	mux.Handle("POST /players/{id}/comments", user(auth.VerifyCSRF(http.HandlerFunc(s.commentSave))))
	mux.Handle("POST /comments/{commentID}/delete", user(auth.VerifyCSRF(http.HandlerFunc(s.commentDelete))))

	// Управление доступами: админ и выше.
	admin := auth.RequireRole(domain.RoleAdmin)
	mux.Handle("GET /users", admin(http.HandlerFunc(s.usersList)))
	mux.Handle("POST /users", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersCreate))))
	mux.Handle("POST /users/{id}/role", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersSetRole))))
	mux.Handle("POST /users/{id}/active", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersSetActive))))
	mux.Handle("POST /users/{id}/password", admin(auth.VerifyCSRF(http.HandlerFunc(s.usersResetPassword))))
	mux.Handle("GET /audit", admin(http.HandlerFunc(s.auditList)))

	// Карточки игроков — админ и выше.
	mux.Handle("GET /players/new", admin(http.HandlerFunc(s.playerNew)))
	mux.Handle("POST /players", admin(auth.VerifyCSRF(http.HandlerFunc(s.playerCreate))))
	mux.Handle("GET /players/{id}/edit", admin(http.HandlerFunc(s.playerEdit)))
	mux.Handle("POST /players/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.playerUpdate))))

	mux.Handle("POST /players/{id}/traits/{traitID}/dropall", admin(auth.VerifyCSRF(http.HandlerFunc(s.traitDropAll))))

	mux.Handle("POST /clans", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanCreate))))
	mux.Handle("POST /clans/{id}", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanUpdate))))
	mux.Handle("POST /clans/{id}/delete", admin(auth.VerifyCSRF(http.HandlerFunc(s.clanDelete))))

	root := auth.RequireRole(domain.RoleRoot)

	// Общие настройки — рутовые: справочник признаков один на всех, и его
	// правка меняет то, что видят остальные. Удаление признака снимает
	// отметки у всех игроков разом, и это тем более не админское дело.
	mux.Handle("GET /settings", root(http.HandlerFunc(s.settingsPage)))
	mux.Handle("POST /settings/coalitions", root(auth.VerifyCSRF(http.HandlerFunc(s.coalitionScanToggle))))
	mux.Handle("POST /settings/traits", root(auth.VerifyCSRF(http.HandlerFunc(s.traitCreate))))
	mux.Handle("POST /settings/traits/{id}", root(auth.VerifyCSRF(http.HandlerFunc(s.traitUpdate))))
	mux.Handle("POST /settings/traits/{id}/delete", root(auth.VerifyCSRF(http.HandlerFunc(s.traitDelete))))
	// Справочник героев обновляется по кнопке: игра его меняет молча,
	// а спрашивать её по таймеру не за чем — новый герой появляется
	// от силы раз в пару месяцев.
	mux.Handle("POST /settings/heroes", root(auth.VerifyCSRF(http.HandlerFunc(s.heroesRefresh))))

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
	// Сбор коалиций ходит в игру от общего аккаунта, поэтому и рубильник
	// у него рутовый. Роут стоит раньше /games/{id}: он точнее.
	mux.Handle("POST /games/{id}/hero", root(auth.VerifyCSRF(http.HandlerFunc(s.gameHeroToggle))))
	mux.Handle("POST /games/{id}/hero/run", root(auth.VerifyCSRF(http.HandlerFunc(s.gameHeroRun))))
	mux.Handle("POST /users/{id}/games", root(auth.VerifyCSRF(http.HandlerFunc(s.usersSetGamesAccess))))
	mux.Handle("POST /users/{id}/checks", root(auth.VerifyCSRF(http.HandlerFunc(s.usersSetMapChecks))))
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
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
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
