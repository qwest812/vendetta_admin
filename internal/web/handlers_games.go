package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/supremacy"
	"Vendetta_admin/internal/supremacy/heroes"
)

// gamesTimeout ограничивает поход в игру: страницу открывает живой человек
// и ждать он готов недолго, а API Supremacy иногда отвечает минутами.
//
// bigGameTimeout — то же для больших партий. Двадцати секунд им не хватает:
// состав на четыреста человек сайт собирает секунд двадцать, и страница
// отваливалась ровно на пороге, ничего не показав. Минута — плата за то,
// чтобы такая партия открылась вообще; человеку об этом говорят на самой
// странице, а следующие шесть часов ответ берётся из кэша.
const (
	gamesTimeout   = 20 * time.Second
	bigGameTimeout = time.Minute
)

// gameSource — откуда берём игры аккаунта. Интерфейс, а не *supremacy.Client,
// чтобы страницу можно было проверить без похода в сеть. Пустое значение
// означает, что аккаунт игры не настроен (S1914_USER не задан).
type gameSource interface {
	MyGames(ctx context.Context) ([]supremacy.Game, error)
	Game(ctx context.Context, gameID string) (*supremacy.Game, []supremacy.GameLogin, error)
	// CachedGame — тот же ответ, но только если он уже лежит в кэше: в сеть
	// не ходит и не ждёт. По нему страница решает, ждать ли сайт вообще.
	CachedGame(gameID string) (*supremacy.Game, []supremacy.GameLogin, bool)
	// CachedState — копия состояния из кэша любого возраста, без захода
	// в партию. Ей дорисовывают карту, которую человеку уже показали.
	CachedState(gameID string) (*supremacy.GameState, bool)
	// StateFor — состояние партии из общего кэша или с игрового сервера,
	// вместе с временем съёмки копии; StateFresh — сколько эта копия
	// считается свежей. Оба знают про скорость партии и про то, что руту
	// нужно свежее остальных.
	StateFor(ctx context.Context, gameID string, ours, root bool) (*supremacy.GameState, time.Time, error)
	StateFresh(gameID string, root bool) time.Duration
	DeployInfantry(ctx context.Context, gameID string) (string, error)
	// GrantRightOfWay заходит в партию и выдаёт право прохода всем ботам,
	// у кого его ещё нет.
	GrantRightOfWay(ctx context.Context, gameID string) (string, error)
	// RallyArmies заходит в партию и шлёт в город все сухопутные армии,
	// RunAutopilot — то же, что делает автопилот за один свой заход.
	RallyArmies(ctx context.Context, gameID string, target int, city string) (*supremacy.RallyRun, error)
	RunAutopilot(ctx context.Context, gameID string, job supremacy.AutopilotJob) (*supremacy.AutopilotRun, error)
	// GameLosses — потери в войнах партии по газете, с первого дня по today.
	GameLosses(ctx context.Context, gameID string, today int) (*supremacy.Losses, error)
	MapGeometry(ctx context.Context, mapID string) (*supremacy.MapGeometry, error)
	// UserStatsBatch — боевой счёт игроков с сайта. Сайт отвечает про
	// одного за раз, поэтому пачка спрашивается в несколько потоков;
	// заходом в партию это не считается.
	UserStatsBatch(ctx context.Context, siteUserIDs []string) (map[string]*supremacy.UserStats, error)
	UserID() string
	// HeroOffers — лестница прокачки героев из витрины магазина игры: по ней
	// справочник героев узнаёт цены уровней. Витрина общая для всех, заходом
	// в партию вопрос не считается.
	HeroOffers(ctx context.Context) ([]heroes.Offer, error)
	// GameSize — состав партии числом, если о ней уже спрашивали. По нему
	// выбирается срок ожидания, см. gameBudget.
	GameSize(gameID string) (int, bool)
}

// gameBudget — сколько ждать игру на этой странице. Размер партии заранее
// неизвестен: его называет тот самый ответ, которого мы ждём. Поэтому
// незнакомую партию считаем большой и даём ей полную минуту, а короткий
// срок оставляем тем, про кого уже точно знаем, что они маленькие.
func (s *Server) gameBudget(gameID string) time.Duration {
	if n, ok := s.games.GameSize(gameID); ok && n < supremacy.BigRoster {
		return gamesTimeout
	}
	return bigGameTimeout
}

// gameView — строка таблицы. Игра отдаёт всё строками, включая время,
// поэтому разбор дат делаем здесь, а не в шаблоне.
type gameView struct {
	ID       string
	Title    string
	State    string
	Day      string
	Players  string
	Language string
	PlayerID string
	Started  time.Time
	Joined   time.Time
}

// gamesList — раздел «Игры». Руту он показывает активные партии общего аккаунта,
// остальным — только форму проверки: список партий говорит, где аккаунт
// играет прямо сейчас, и это знание рутовое. Проверить же партию по номеру
// может любой, кому доступ к разделу выдан: и сведения о партии, и её состав
// с картой берутся так, что заходом в партию это не считается.
func (s *Server) gamesList(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	data := map[string]any{
		"Games": nil, "Error": "", "PlayURL": "", "CheckError": "",
		"Query": strings.TrimSpace(r.URL.Query().Get("id")),
		// Coalitions — сводка архива и состояние его рубильника. Только
		// руту: обход ходит в игру от общего аккаунта проекта.
		"Coalitions": nil,
		// Hunts — поиск игроков в лобби: кого ищем и где нашли. Только руту.
		"Hunts": nil,
		// Checked — партии, которые смотрел тот, кто сейчас на странице,
		// и через сколько дней они из списка уходят.
		"Checked": nil, "CheckedDays": int(domain.CheckedGamesTTL.Hours() / 24),
		// Проверки карты: сколько их в сутки и сколько осталось на сегодня.
		// Руту не считают — ему и показывать нечего.
		"ChecksTotal": 0, "ChecksLeft": 0, "ChecksCounted": false,
	}

	// Список проверенных партий читается из базы и есть у всех, кому открыт
	// раздел: он ничего не спрашивает у игры, поэтому считается до неё —
	// даже когда игровой аккаунт не настроен, список остаётся на месте.
	checked, err := s.checked.List(r.Context(),
		currentUser(r).ID, time.Now().Add(-domain.CheckedGamesTTL))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Checked"] = checked

	// Остаток проверок на сегодня — там же, откуда партии открывают.
	if me := currentUser(r); !me.IsRoot() {
		left, err := s.checks.Left(r.Context(), me.ID, me.MapChecks, repo.Day(time.Now()))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["ChecksCounted"] = true
		data["ChecksTotal"] = me.MapChecks
		data["ChecksLeft"] = left
	}

	if s.games == nil {
		data["Error"] = lang.T("games.noaccount")
		s.render(w, r, http.StatusOK, "games", data)
		return
	}
	if msg := r.URL.Query().Get("err"); msg != "" {
		data["CheckError"] = lang.T("games.badnumber")
	}

	// Не рут дальше формы не идёт: за списком партий мы ради него в игру
	// не ходим вовсе.
	if !currentUser(r).IsRoot() {
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	// Архив коалиций читается из базы и молчания игры не боится, поэтому
	// считается до похода за списком партий: даже когда игра не ответит,
	// рубильник останется на месте.
	if s.coalitions != nil && s.settings != nil {
		view, err := s.coalitionCard(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Coalitions"] = view
	}

	// Поиск игроков тоже целиком из базы — считается до похода в игру.
	if s.hunts != nil {
		view, err := s.huntCard(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Hunts"] = view
	}

	games, err := s.games.MyGames(ctx)
	if err != nil {
		// Отказ игры — это не поломка админки: показываем причину на самой
		// странице, чтобы было видно, когда протухла сессия или лежит API.
		s.log.Error("список игр Supremacy", "err", err)
		data["Error"] = lang.T("games.list.error", err)
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	data["Games"] = gameViews(lang, games)
	data["PlayURL"] = supremacy.PlayURL(s.games.UserID())
	s.render(w, r, http.StatusOK, "games", data)
}

var reDigits = regexp.MustCompile(`\d+`)

// gameIDFrom достаёт номер партии из того, что ввели. Приносят и голый номер,
// и ссылку целиком, поэтому берём самое длинное число: в адресе игры есть
// и другие — хотя бы 1914 в самом имени сайта. Короткие обрывки за номер
// не считаем вовсе.
func gameIDFrom(input string) string {
	var best string
	for _, digits := range reDigits.FindAllString(input, -1) {
		if len(digits) > len(best) {
			best = digits
		}
	}
	if len(best) < 4 {
		return ""
	}
	return best
}

// gamesCheck — кнопка «Проверить игру»: по введённому номеру уводит
// на страницу партии. Отдельный роут нужен затем, чтобы адрес партии
// оставался прежним и им можно было делиться.
//
// Рута уводит сразу за состоянием (state=1): за картой и составом он и ходит,
// и ждать второго нажатия ему незачем. Остальных — на обычную страницу
// партии: в своей партии заход стоит настоящего входа, и делать его за
// человека, который просто набрал номер, мы не будем. Кнопка «Заглянуть
// в партию» на странице остаётся для всех.
func (s *Server) gamesCheck(w http.ResponseWriter, r *http.Request) {
	id := gameIDFrom(r.URL.Query().Get("id"))
	if id == "" {
		http.Redirect(w, r, "/games?err=1", http.StatusSeeOther)
		return
	}
	to := "/games/" + id
	if currentUser(r).IsRoot() {
		to += "?state=1"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// gameViews готовит игры к показу: свежая партия сверху.
func gameViews(l i18n.Lang, games []supremacy.Game) []gameView {
	out := make([]gameView, 0, len(games))
	for _, g := range games {
		out = append(out, gameView{
			ID:       g.GameID,
			Title:    g.Title,
			State:    gameState(l, g.State),
			Day:      g.DayOfGame,
			Players:  g.NrOfPlayers,
			Language: g.Language,
			PlayerID: g.PlayerID,
			Started:  g.Started(),
			Joined:   g.Joined(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// gameState переводит состояние партии. Незнакомое оставляем как есть:
// лучше показать слово игры, чем скрыть его пустотой.
func gameState(l i18n.Lang, state string) string {
	switch state {
	case "running":
		return l.T("gamestate.running")
	case "readytojoin":
		return l.T("gamestate.readytojoin")
	case "finished", "ended":
		return l.T("gamestate.finished")
	}
	return state
}

// gameAbout и gameLive — ответы двух походов наружу, которые страница партии
// делает одновременно. Каналы под них буферизованные: если страница сдалась
// раньше (сайт молчит, номер выдуман), горутина допишет ответ в буфер
// и завершится, а не повиснет навсегда. Ответ сайта, которого страница
// ждать не стала, дочитывает фон — см. finishAbout.
type gameAbout struct {
	game   *supremacy.Game
	roster []supremacy.GameLogin
	err    error
}

type gameLive struct {
	state *supremacy.GameState
	// at — когда сняли копию состояния. Может быть заметно раньше запроса:
	// копия общая, и привезти её мог кто угодно.
	at  time.Time
	err error
}

// setMapWait кладёт на страницу таймер до следующей копии: секунды тикает
// скрипт, готовую строку видит тот, у кого скрипта нет.
func setMapWait(data map[string]any, left time.Duration) {
	sec := waitSeconds(left)
	data["MapWait"] = sec
	data["MapWaitText"] = waitClock(sec)
}

// gameCard — страница одной партии: что это за партия и что админка делает
// в ней сама. Состав партии сюда не тянется по умолчанию: заход на игровой
// сервер игра засчитывает как вход в партию, поэтому его просят отдельно.
func (s *Server) gameCard(w http.ResponseWriter, r *http.Request) {
	s.renderGame(w, r, r.PathValue("id"), r.URL.Query().Get("state") == "1", "")
}

func (s *Server) renderGame(w http.ResponseWriter, r *http.Request, gameID string, withState bool, errMsg string) {
	s.renderGameNotice(w, r, gameID, withState, errMsg, "")
}

// renderGameNotice — то же, что renderGame, плюс строка об успехе: её
// показывает блок «Мы», под кнопками, которые её вызвали.
func (s *Server) renderGameNotice(w http.ResponseWriter, r *http.Request, gameID string, withState bool, errMsg, notice string) {
	// Засекаем сразу: «собираем данные» показывается ровно столько, сколько
	// не хватило странице до обычного времени сбора. См. loaderMs.
	started := time.Now()

	if s.games == nil {
		s.render(w, r, http.StatusOK, "game", map[string]any{
			"GameID": gameID,
			"Error":  langOf(r).T("games.noaccount"),
		})
		return
	}

	lang := langOf(r)

	task, err := s.tasks.Get(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Очередь строительства лежит в нашей базе и показывается без всякого
	// захода в партию. Провинции и справочник зданий к ней добавит fillLive,
	// если до состояния партии дело дойдёт.
	builds, err := s.buildData(r, gameID, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := map[string]any{
		"GameID": gameID, "Task": task, "Error": errMsg, "Notice": notice,
		"Interval": s.heroEvery, "IntervalMax": s.heroEveryMax,
		"Game": nil, "State": nil, "Mine": nil,
		"Map": nil, "MapError": "", "Enemies": nil, "Friends": nil,
		"Premium": nil, "Banned": nil, "Roster": nil, "RosterPages": nil,
		// PowerKnown — есть ли у смотрящего свой боевой счёт. Без него
		// колонка силы в составе пуста, и таблица говорит об этом сама.
		"PowerKnown": false,
		// AlliancesPending — сколько игроков партии воркер ещё не спросил.
		"AlliancesPending": 0,
		// Pairs — кто из игроков этой партии уже союзничал раньше.
		"Pairs": nil,
		// Ours — играет ли общий аккаунт в этой партии. От этого зависит,
		// как мы смотрим на неё: в свою заходим игроком по кнопке, в чужую
		// — наблюдателем и сразу, потому что входом в партию это не считается.
		"Ours": false,
		// Своя страна — от профиля смотрящего. Её может не быть по двум
		// разным причинам, и сказать об этом надо по-разному.
		"Me": nil, "NoProfileGameID": false, "NotPlaying": false,
		// Когда сняли копию состояния и сколько ждать новой: секундами
		// для скрипта, строкой для глаз. Плюс адрес, по которому просить
		// её заново.
		"MapAt": time.Time{}, "MapWait": 0, "MapWaitText": "",
		// Проверки: сколько их в сутки у смотрящего, сколько осталось,
		// считают ли их ему вообще и не кончились ли они прямо сейчас.
		"ChecksTotal": 0, "ChecksLeft": 0, "ChecksCounted": false, "ChecksOut": false,
		"AnonymousClosed": false,
		// LoaderMs — сколько держать «собираем данные». Ноль — не держать.
		"LoaderMs": 0,
		// BigGame — состав большой партии числом; ноль значит «обычная».
		"BigGame": 0,
		// RosterLater — карта нарисована без состава с сайта, и страница
		// попросит его отдельным запросом. См. gameRoster.
		"RosterLater": false,
	}
	merge(data, builds)

	ctx, cancel := context.WithTimeout(r.Context(), s.gameBudget(gameID))
	defer cancel()

	// Сведения о партии с сайта — самый долгий поход из всех: партию на
	// пятьсот игроков сайт собирает секунд двадцать. Лежат в кэше — берём
	// сразу; нет — отправляемся за ними, не дожидаясь остального.
	game, roster, rosterKnown := s.games.CachedGame(gameID)
	var about chan gameAbout
	if !rosterKnown {
		about = s.fetchAbout(r, gameID)
	}

	// Название и день берём из дешёвого списка партий: он же подтверждает,
	// что партия наша и ещё идёт.
	games, err := s.games.MyGames(ctx)
	if err != nil {
		data["Error"] = joinErrors(errMsg, lang.T("games.list.error", err))
		s.render(w, r, http.StatusOK, "game", data)
		return
	}
	// mine — сама партия из списка своих, а не только её вид: архиву
	// коалиций нужна скорость, а её вид не несёт.
	var mine *supremacy.Game
	for i, g := range games {
		if g.GameID == gameID {
			mine = &games[i]
			view := gameViews(lang, []supremacy.Game{g})[0]
			data["Game"] = &view
			data["Ours"] = true
			break
		}
	}
	ours := data["Ours"] == true

	// Состояние партии — второй поход, и ему хватает уже известного: свою
	// партию открываем игроком, чужую смотрим наблюдателем. Отправляем его
	// сейчас, чтобы он шёл под ответ сайта, а не после него.
	//
	// Показ карты стоит проверки: рут выдаёт их на сутки, и тратятся они
	// на каждый показ — даже когда данные пришли из общего кэша. Человек
	// смотрит партию, а откуда взялись данные, его не касается.
	who := currentUser(r)
	root := who.IsRoot()
	day := repo.Day(time.Now())
	data["ChecksCounted"] = !root
	data["ChecksTotal"] = who.MapChecks

	// Анонимную партию разбирает только ультра. В такой партии игра прячет
	// имя каждого участника — а мы их всё равно называем: скрыт только ник,
	// номер аккаунта настоящий, и состав с сайта по нему называет живого
	// человека. Снятая анонимность — самое острое, что админка умеет,
	// и раздаётся она не всем.
	//
	// Закрыта вся страница, а не одна карта: за данными о партии сюда
	// и приходят, и состав с сайта — те же самые имена, только без карты.
	//
	// Про свою партию это известно уже сейчас, из списка своих игр, поэтому
	// в неё мы даже не заходим: заход игра засчитывает как вход, и тратить
	// его на страницу, которой человек не увидит, незачем. Про чужую скажет
	// её состояние или сайт — там та же проверка ещё раз.
	if mine != nil && mine.IsAnonymous() && !who.CanSeeAnonymousMaps() {
		s.anonymousClosed(w, r, data, gameID)
		return
	}

	var (
		live  chan gameLive
		spent bool
	)
	switch {
	case withState || !ours:
		if root {
			// Лимит рута не касается, но в счёт открытий он входит: иначе
			// аналитика показывала бы всех, кроме самого активного.
			if err := s.checks.Record(ctx, who.ID, day); err != nil {
				s.serverError(w, r, err)
				return
			}
			spent = true
		} else {
			ok, left, err := s.checks.Spend(ctx, who.ID, who.MapChecks, day)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			data["ChecksLeft"] = left
			if !ok {
				// Проверки кончились — карты не будет. Остальное собирается
				// ниже: сведения с сайта ничего не стоят.
				data["ChecksOut"] = true
				break
			}
			spent = true
		}
		live = make(chan gameLive, 1)
		go func() {
			state, at, err := s.games.StateFor(ctx, gameID, ours, root)
			live <- gameLive{state: state, at: at, err: err}
		}()
	case !root:
		// В свою партию не ходили — её открывают кнопкой. Остаток проверок
		// надо показать до того, как человек в него упрётся.
		left, err := s.checks.Left(ctx, who.ID, who.MapChecks, day)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["ChecksLeft"] = left
	}

	small := false
	if n, ok := s.games.GameSize(gameID); ok && n < supremacy.BigRoster {
		small = true
	}
	wait, later := siteWait(rosterKnown, live != nil, small, ours)

	// Страница, которая сайт ждать не стала, отдаёт его ответ в фон: там
	// кланы из состава лягут в базу, игроки — с никами от сайта, а чужая
	// партия — в список проверенных. Состояние к этому времени уже известно
	// (или его не будет), поэтому фон зовётся на выходе.
	var state *supremacy.GameState
	if later {
		defer func() { go s.finishAbout(about, who, gameID, mine, state) }()
	}

	if wait {
		var got gameAbout
		select {
		case got = <-about:
		case <-ctx.Done():
			got.err = ctx.Err()
		}
		game, roster, err = got.game, got.roster, got.err
		if err != nil {
			s.log.Warn("сведения о партии", "gameID", gameID, "err", err)
			// Не дождались — случай отдельный от всех прочих: партия жива,
			// просто велика, и говорить про неё «не нашлось» неправда. Номер
			// в такой ответ не идёт: человек его только что ввёл сам.
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				data["Error"] = joinErrors(errMsg,
					lang.T("game.slow", waitSeconds(s.gameBudget(gameID))))
			// Своя партия переживёт молчание сайта: название и день уже есть
			// из списка, а альянсы возьмутся из базы. Чужая — нет, про неё мы
			// больше ничего и не знаем.
			case data["Game"] == nil:
				data["Error"] = joinErrors(errMsg, lang.T("game.notfound", gameID, err))
			default:
				data["Error"] = joinErrors(errMsg, lang.T("game.roster.error", err))
			}
			if data["Game"] == nil {
				s.render(w, r, http.StatusOK, "game", data)
				return
			}
		}
		rosterKnown = err == nil
	}

	// Та же проверка во второй раз: про чужую партию, что она анонимная,
	// говорит сайт, и раньше этой строки мы этого не знали. Стоит она
	// прежде всего остального — ни состав, ни кланы со страницы уже
	// не соберутся.
	//
	// Состояние к этому времени уже поехало: для чужой партии это наблюдение,
	// следа в ней оно не оставляет. Ответ допишется в буфер канала, горутина
	// завершится, а нам он больше не нужен.
	if game != nil && game.IsAnonymous() && !who.CanSeeAnonymousMaps() {
		s.closeAnonymous(w, r, data, gameID, spent)
		return
	}

	if rosterKnown {
		// Большая партия открывается ощутимо дольше маленькой, и человеку об
		// этом стоит сказать самому: иначе долгое ожидание выглядит поломкой.
		// Заодно объясняем, почему второй заход мгновенный.
		if n := len(roster); n >= supremacy.BigRoster {
			data["BigGame"] = n
		}
		// Кланы из состава кладём в базу независимо от того, дошло ли дело
		// до карты: раз уж сайт их назвал, пусть остаются.
		if err := s.rememberAlliances(ctx, roster); err != nil {
			s.log.Error("запись кланов из состава", "gameID", gameID, "err", err)
		}
		// Чужая партия — не ошибка: название и состояние сайт называет про любую.
		if !ours {
			view := gameViews(lang, []supremacy.Game{*game})[0]
			data["Game"] = &view
		}
	}

	// Партия нашлась — значит, она проверена, и место ей в личном списке
	// того, кто смотрит. Отметка ставится и на чужую партию, и на свою
	// до захода внутрь: проверкой считается сам взгляд на партию, а не то,
	// как далеко он зашёл. Название пишем то, что видно сейчас: партия
	// когда-нибудь кончится, а список должен читаться и без похода в игру.
	// Чужую, которую сайт ещё не назвал, отметит фон — без названия
	// отметка в списке читалась бы номером.
	if view, ok := data["Game"].(*gameView); ok {
		if err := s.checked.Mark(ctx, who.ID, gameID, view.Title, time.Now()); err != nil {
			// Список — удобство, а не суть страницы: не вышло записать,
			// значит партия просто не всплывёт наверх.
			s.log.Error("запись проверенной партии", "gameID", gameID, "err", err)
		}
	}

	// В свою партию заходим только по кнопке: такой заход игра засчитывает
	// как вход. В чужую смотрим наблюдателем, и вот это уже ничего не стоит
	// — значит, и прятать за кнопкой незачем.
	if live != nil {
		res := <-live
		if res.err != nil {
			// Проверку возвращаем: человек ничего не увидел, брать за это
			// плату нечестно. Не вышло вернуть — не беда, скажем в лог.
			if spent {
				if err := s.checks.Refund(ctx, who.ID, day); err != nil {
					s.log.Error("возврат проверки карты", "user_id", who.ID, "err", err)
				}
				data["ChecksLeft"] = data["ChecksLeft"].(int) + 1
			}
			s.log.Warn("состояние партии", "gameID", gameID, "наша", ours, "err", res.err)
			if ours {
				data["Error"] = joinErrors(errMsg, lang.T("game.enter.error", res.err))
			} else {
				data["Error"] = joinErrors(errMsg, lang.T("game.observe.error", res.err))
			}
			s.render(w, r, http.StatusOK, "game", data)
			return
		}

		// Анонимность называет и само состояние — для чужой партии, которую
		// сайт ещё не назвал, это единственный способ узнать её до показа.
		// Состояние запоминаем до проверки: фон по нему тоже поймёт, что
		// с закрытой партии записывать нечего.
		state = res.state
		if state.Anonymous && !who.CanSeeAnonymousMaps() {
			s.closeAnonymous(w, r, data, gameID, spent)
			return
		}

		data["State"] = state
		// Копия общая, и снята она могла быть заметно раньше — скажем,
		// когда именно, и когда можно будет взять новую.
		data["MapAt"] = res.at
		setMapWait(data, time.Until(res.at.Add(s.games.StateFresh(gameID, root))))

		// Раз уж состав перед глазами — запомним про игроков то, что
		// принадлежит им самим, а не этой партии. Ник берётся у сайта,
		// поэтому без состава это дело фона.
		//
		// Коалиции этой партии оседают в архиве раньше, чем архив спросят,
		// кто здесь уже союзничал. Сведения о партии берём откуда есть —
		// у своей они из списка своих, у чужой из ответа сайта.
		if rosterKnown {
			about := nonNilGame(game, mine)
			anonymous := about != nil && about.IsAnonymous()
			if err := s.rememberPlayers(ctx, state, roster, anonymous); err != nil {
				s.log.Error("запись состава партии", "gameID", gameID, "err", err)
			}
		}
		if about := nonNilGame(game, mine); about != nil {
			s.rememberCoalitions(ctx, about, state)
		}

		if err := s.fillLive(ctx, r, data, state, roster, later); err != nil {
			s.serverError(w, r, err)
			return
		}
		if data["Map"] != nil {
			data["LoaderMs"] = loaderMs(time.Since(started))
			if later {
				data["RosterLater"] = true
				s.mapTickets.grant(who.ID, gameID)
			}
		}
	}

	s.render(w, r, http.StatusOK, "game", data)
}

// siteWait решает, ждать ли странице ответ сайта о партии. wait — ждать
// сейчас; later — не ждать, а дочитать ответ в фоне. Оба ложны, когда ответ
// уже лежит в кэше.
//
// Карта от сайта не зависит: кланы для раскраски лежат в базе, анонимность
// называет само состояние. Поэтому большую или незнакомую партию рисуем
// сразу, а состав страница попросит следом. Маленькую ждём, как раньше:
// сайт отдаёт её за секунду, и второй запрос обошёлся бы дороже ожидания.
//
// Без карты свою партию не ждём тоже — название уже есть из списка своих.
// Чужую без карты ждём всегда: кроме сайта про неё сказать нечего.
func siteWait(cached, withMap, small, ours bool) (wait, later bool) {
	if cached {
		return false, false
	}
	wait = (withMap && small) || (!withMap && !ours)
	return wait, !wait
}

// nonNilGame — сведения о партии, какие есть: ответ сайта или своя партия
// из списка своих. Пусто, когда нет ни того, ни другого.
func nonNilGame(site, mine *supremacy.Game) *supremacy.Game {
	if site != nil {
		return site
	}
	return mine
}

// closeAnonymous закрывает страницу анонимной партии и возвращает проверку,
// если её уже взяли: человек ничего не увидел, брать за это плату нечестно.
func (s *Server) closeAnonymous(w http.ResponseWriter, r *http.Request, data map[string]any,
	gameID string, spent bool) {

	if spent {
		who := currentUser(r)
		if err := s.checks.Refund(r.Context(), who.ID, repo.Day(time.Now())); err != nil {
			s.log.Error("возврат проверки карты", "user_id", who.ID, "err", err)
		}
	}
	s.anonymousClosed(w, r, data, gameID)
}

// fetchAbout отправляет за сведениями о партии и составом. Поход отвязан
// от запроса страницы: большую партию сайт отдаёт дольше, чем страница
// соглашается ждать, а привезённое ляжет в кэш — оттуда его возьмёт запрос
// состава, который страница пошлёт следом. Срок у похода свой, тот же,
// что страница дала бы ему сама.
func (s *Server) fetchAbout(r *http.Request, gameID string) chan gameAbout {
	about := make(chan gameAbout, 1)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.gameBudget(gameID))
	go func() {
		defer cancel()
		g, roster, err := s.games.Game(ctx, gameID)
		about <- gameAbout{game: g, roster: roster, err: err}
	}()
	return about
}

// finishAbout дожидается сайта за страницу, которая ждать не стала, и делает
// с его ответом то, что страница сделала бы сама: кланы из состава — в базу,
// игроков партии — с никами от сайта, коалиции и отметку о проверке чужой
// партии — туда же. Человеку сам состав покажет отдельный запрос, а это
// должно случиться, даже если он закрыл страницу, не дождавшись.
//
// state — состояние, если страница его получила; mine — своя партия из
// списка своих, если партия наша: тогда отметку и коалиции страница уже
// записала сама.
func (s *Server) finishAbout(about <-chan gameAbout, who *domain.User, gameID string,
	mine *supremacy.Game, state *supremacy.GameState) {

	got := <-about
	if got.err != nil {
		s.log.Warn("сведения о партии в фоне", "gameID", gameID, "err", got.err)
		return
	}
	// Анонимную партию смотреть не дали — и запоминать с неё нечего.
	anonymous := got.game.IsAnonymous() || (state != nil && state.Anonymous)
	if anonymous && !who.CanSeeAnonymousMaps() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), gamesTimeout)
	defer cancel()

	if err := s.rememberAlliances(ctx, got.roster); err != nil {
		s.log.Error("запись кланов из состава", "gameID", gameID, "err", err)
	}
	if mine == nil {
		if err := s.checked.Mark(ctx, who.ID, gameID, got.game.Title, time.Now()); err != nil {
			s.log.Error("запись проверенной партии", "gameID", gameID, "err", err)
		}
	}
	if state == nil {
		return
	}
	if err := s.rememberPlayers(ctx, state, got.roster, anonymous); err != nil {
		s.log.Error("запись состава партии", "gameID", gameID, "err", err)
	}
	if mine == nil {
		s.rememberCoalitions(ctx, got.game, state)
	}
}

// fillLive собирает всё, что страница показывает по состоянию партии: карту
// во всех режимах, состав, личные списки, премиум, баны и старые союзы.
// Зовут её двое: сама страница и запрос состава, который дорисовывает карту
// большой партии, когда сайт наконец ответит.
//
// later — состава с сайта пока нет, и он приедет отдельно. Тогда кланы для
// раскраски берутся только из базы, а незнакомых не ставят в очередь.
func (s *Server) fillLive(ctx context.Context, r *http.Request, data map[string]any,
	state *supremacy.GameState, roster []supremacy.GameLogin, later bool) error {

	lang := langOf(r)
	who := currentUser(r)

	// Своя страна ищется по игровому ID из профиля: у каждого смотрящего
	// она своя, а у кого-то её в этой партии и нет вовсе.
	viewer := who.GameID
	me, playing := viewerPlayer(state, viewer)
	meID := 0
	switch {
	case playing:
		meID = me.ID
		mine := state.Owned(meID)
		sort.Slice(mine, func(i, j int) bool {
			if mine[i].Capital != mine[j].Capital {
				return mine[i].Capital
			}
			return mine[i].Name < mine[j].Name
		})
		player := me
		data["Me"] = &player
		data["Mine"] = mine
	case viewer == "":
		data["NoProfileGameID"] = true
	default:
		data["NotPlaying"] = true
	}

	// Игроков партии сводим с базой по игровому ID и отмечаем тех,
	// кто в личных списках того, кто смотрит.
	sides, err := s.gameLists(r, state)
	if err != nil {
		return err
	}
	data["Enemies"] = relationViews(state, sides.Enemies)
	data["Friends"] = relationViews(state, sides.Friends)
	data["Premium"] = premiumViews(lang, state, meID)
	data["Banned"] = bannedViews(lang, state)

	// Кланы игроков берутся из самой игры, но не сейчас: страница
	// читает уже известное, а незнакомых ставит в очередь воркеру.
	alliances, pending, err := s.gameAlliances(ctx, state, roster, !later)
	if err != nil {
		return err
	}
	sides.Alliances = alliances
	data["AlliancesPending"] = pending

	// Боевой счёт участников и смотрящего: из базы, а устаревшее
	// пересчитывается прямо сейчас. Своё число нужно вместе с чужими,
	// иначе сравнивать не с чем.
	stats, myStats, err := s.gameStats(ctx, state, who.GameID)
	if err != nil {
		return err
	}
	sides.Stats, sides.Me = stats, myStats

	// Состав показывает шаблон во вкладке «Сила» под картой. Страны и счёт
	// дописываются после того, как счёт пересчитан: иначе таблица показала
	// бы прочерки там, где числа только что приехали.
	views, powerKnown, err := s.rosterViews(r, roster)
	if err != nil {
		return err
	}
	rosterFromState(views, state, sides)
	data["Roster"] = views
	data["RosterPages"] = rosterPages(views)
	data["PowerKnown"] = powerKnown || sides.Me.Rated()

	// Верхушка рейтинга — десяток строк на всю админку, поэтому
	// читается целиком и на каждую страницу. Её отсутствие карту
	// не ломает: режим топа просто останется серым.
	top, err := s.topAlliances.All(ctx)
	if err != nil {
		return err
	}
	sides.Top = make(map[string]domain.TopAlliance, len(top))
	for _, t := range top {
		sides.Top[t.ID] = t
	}
	at := topCapturedAt(top)
	data["TopAt"] = at
	data["TopKnown"] = !at.IsZero()

	// Обратный вопрос к архиву коалиций: кто из здешних уже союзничал.
	pairs, err := s.coalitionPairs(r, state, sides)
	if err != nil {
		return err
	}
	data["Pairs"] = pairs
	sides.Together, sides.TogetherLegend = togetherGroups(pairs)

	// Экономика и производство всех игроков — из того же состояния,
	// отдельного захода в партию не нужно.
	data["Economy"] = economy(lang, state, meID)

	// Очередь строительства: сама очередь уже в данных страницы, а здесь
	// к ней добавляются свои провинции и справочник зданий — их знает
	// только состояние партии.
	if builds, err := s.buildData(r, state.GameID, state); err == nil {
		merge(data, builds)
	} else {
		return err
	}
	// Город для сбора войск выбирается из своих провинций — их знает
	// только состояние партии.
	data["RallyProvinces"] = rallyProvinces(state)

	// Карту рисуем, если получилось: очертания лежат отдельным файлом
	// на static-сервере игры, и его недоступность не повод прятать
	// остальную страницу.
	if geo, err := s.games.MapGeometry(ctx, state.MapID); err != nil {
		s.log.Warn("очертания карты", "gameID", state.GameID, "mapID", state.MapID, "err", err)
		data["MapError"] = lang.T("game.map.error", err)
	} else {
		data["Map"] = gameMap(lang, geo, state, sides, meID)
	}
	return nil
}

// joinErrors склеивает предупреждение и ошибку: на странице место одно,
// а сказать иногда нужно и то и другое.
func joinErrors(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "; ")
}
