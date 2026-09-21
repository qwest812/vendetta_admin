package supremacy

// Архив коалиций: кто с кем состоял в союзе в прошлых партиях. Обход ходит
// в партию наблюдателем — входом в неё это не считается, и партия про такой
// заход не узнаёт.
//
// Момент съёма выбран посреди жизни партии, а не в её конце, и это главное
// решение здесь. К финалу коалиции распускают, а вместе с роспуском пропадает
// состав: в завершённой партии остаются одни названия и ни одного участника.
// Поэтому расписание считает не «когда партия кончится», а «когда она будет
// в разгаре».

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"Vendetta_admin/internal/domain"
)

// CoalitionStore — где живёт очередь обхода и то, что в ней нашлось.
type CoalitionStore interface {
	Enqueue(ctx context.Context, g domain.WatchedGame) error
	// Due — кому пора; urgent — только срочным (эндшпиль), иначе всем.
	Due(ctx context.Context, at time.Time, limit int, urgent bool) ([]domain.WatchedGame, error)
	Save(ctx context.Context, gameID string, day int, teams []domain.Coalition, at, next time.Time, done, urgent bool) error
	Fail(ctx context.Context, gameID, reason string, at, next time.Time, done bool) error
}

// CoalitionSwitch — общий выключатель обхода. Рут должен уметь остановить
// его, не дожидаясь пересборки образа. Ключ настройки воркеру не показываем:
// его дело — спросить «можно ли», а не знать, где записан ответ.
type CoalitionSwitch interface {
	CoalitionScanEnabled(ctx context.Context) (bool, error)
}

// gameObserver — тот, кто умеет посмотреть партию со стороны. Интерфейс
// ради тестов: в бою сюда приходит *Client.
type gameObserver interface {
	ObserveGame(ctx context.Context, gameID string) (*GameState, error)
}

// coalitionPlan — когда впервые заглянуть в партию той или иной скорости.
// Списком, а не формулой: сроки взяты из наблюдений за живыми партиями.
// Первый заход не ради состава — коалиции собирают под конец, — а чтобы
// узнать счёт и дальше идти по нему (см. planNext).
type coalitionPlan struct {
	minSpeed float64
	first    time.Duration
}

// Замеренная жизнь партий: x10 — около 34 часов, x4 — около четырёх суток,
// x1 — два-три месяца. Первый раз заходим примерно на середине.
var coalitionPlans = []coalitionPlan{
	{minSpeed: 8, first: 16 * time.Hour},
	{minSpeed: 3, first: 48 * time.Hour},
	{minSpeed: 1.5, first: 5 * 24 * time.Hour},
	{minSpeed: 0, first: 30 * 24 * time.Hour},
}

const (
	// Коалиции игроки собирают под конец: очки коалиции — сумма очков её
	// участников, и объединяются, чтобы вместе перешагнуть порог. Победу
	// игра объявляет только при смене дня, поэтому конец партии всегда
	// приходится на смену дня, и время ближайшей известно заранее.
	//
	// В эндшпиле заходим раз за игровой день — за endgameLead до смены,
	// застать собранные к ней коалиции. Отдельного захода после смены нет:
	// что партия кончилась, скажет следующий такой же заход, а состав
	// победившей коалиции игра показывает и после конца.
	endgameLead = 10 * time.Minute

	// Эндшпиль — когда кто-то подошёл к порогу на endgameShare или когда
	// endgameTop сильнейших игроков вместе уже набирают порог коалиции,
	// то есть могут объединиться и выиграть. Оба числа — оценка, а не
	// правило игры: сколько человек бывает в коалиции, игра не говорит.
	endgameShare = 0.6
	endgameTop   = 5

	// midgameDays — сколько игровых дней между заходами до эндшпиля:
	// следим за счётом, но нечасто.
	midgameDays = 2

	// coalitionRetry — через сколько повторить заход, если игра не ответила.
	// В эндшпиле ждать нельзя: смена дня не подождёт.
	coalitionRetry        = 2 * time.Hour
	coalitionRetryEndgame = 3 * time.Minute

	// coalitionRetries — сколько неудач подряд терпим, прежде чем бросить
	// партию. Без этого потолка мёртвая партия висела бы в очереди вечно.
	coalitionRetries = 2

	// coalitionMaxChecks — страховка от партии, которая почему-то не
	// кончается: обычной хватает с запасом.
	coalitionMaxChecks = 120
)

func planFor(speed float64) coalitionPlan {
	for _, p := range coalitionPlans {
		if speed >= p.minSpeed {
			return p
		}
	}
	return coalitionPlans[len(coalitionPlans)-1]
}

// FirstCheck — когда впервые заглянуть в партию, увиденную в лобби.
func FirstCheck(speed float64, from time.Time) time.Time {
	return from.Add(planFor(speed).first)
}

// Endgame — подошла ли партия к концу: кто-то близок к порогу или
// сильнейшие вместе уже могут его взять.
func Endgame(g *GameState) bool {
	r := g.Race
	near := func(points, limit int) bool {
		return limit > 0 && float64(points) >= endgameShare*float64(limit)
	}
	for _, p := range r.TeamPoints {
		if near(p, r.TeamWinPoints) {
			return true
		}
	}
	var human []int
	for id, p := range r.Points {
		if near(p, r.WinPoints) {
			return true
		}
		if pl, ok := g.Players[id]; ok && !pl.IsAI {
			human = append(human, p)
		}
	}
	if r.TeamWinPoints <= 0 {
		return false
	}
	sort.Sort(sort.Reverse(sort.IntSlice(human)))
	sum := 0
	for i, p := range human {
		if i == endgameTop {
			break
		}
		sum += p
	}
	return sum >= r.TeamWinPoints
}

// planNext — когда возвращаться после удачного захода, срочно ли это
// и не пора ли отпустить партию.
func planNext(g domain.WatchedGame, st *GameState, now time.Time) (next time.Time, done, urgent bool) {
	if st.Ended || g.Checks+1 >= coalitionMaxChecks {
		return now, true, false
	}
	speed := g.Speed
	if speed < 1 {
		speed = 1
	}
	gameDay := time.Duration(float64(24*time.Hour) / speed)

	if Endgame(st) {
		// Смена дня неизвестна или уже прошла (состояние отстало) —
		// заглянем через четверть игрового дня.
		if st.NextDay.IsZero() || !st.NextDay.After(now) {
			return now.Add(gameDay / 4), false, true
		}
		lead := st.NextDay.Add(-endgameLead)
		// Мы уже перед самой сменой — следующий раз перед следующей:
		// игровой день спустя.
		if !lead.After(now.Add(time.Minute)) {
			lead = lead.Add(gameDay)
		}
		return lead, false, true
	}
	return now.Add(midgameDays * gameDay), false, false
}

// CoalitionScanner берёт партии, которым пора, и записывает их коалиции.
//
// Проверяет очередь раз в минуту, но обычные партии берёт не чаще раза
// в every: им спешить некуда. Срочные — заходы перед сменой дня в эндшпиле —
// берутся на каждой проверке и вне очереди: опоздание на десять минут
// там означает опоздание к концу партии.
type CoalitionScanner struct {
	client   gameObserver
	store    CoalitionStore
	settings CoalitionSwitch
	log      *slog.Logger

	every time.Duration
	// batch — сколько обычных партий берём за раз. Обход намеренно
	// неспешный: состояние партии весит полтора мегабайта.
	batch int
	// urgentBatch — сколько срочных за одну проверку.
	urgentBatch int
	// pause — сколько ждём между партиями внутри одной проверки.
	pause time.Duration

	lastRegular time.Time
	now         func() time.Time
}

// scanTick — как часто проверять очередь. Это запрос к своей базе,
// а не заход в игру.
const scanTick = time.Minute

func NewCoalitionScanner(c gameObserver, store CoalitionStore, settings CoalitionSwitch,
	every time.Duration, log *slog.Logger) *CoalitionScanner {

	return &CoalitionScanner{
		client: c, store: store, settings: settings, log: log,
		every: every, batch: 5, urgentBatch: 10, pause: 3 * time.Second, now: time.Now,
	}
}

// Enqueue ставит в очередь партии, увиденные в лобби. Уже известные
// хранилище пропустит само: у них своё расписание.
func (s *CoalitionScanner) Enqueue(ctx context.Context, games []Game) {
	now := s.now()
	for _, g := range games {
		speed := g.Speed()
		err := s.store.Enqueue(ctx, domain.WatchedGame{
			GameID:      g.GameID,
			Title:       g.Title,
			Language:    g.Language,
			Speed:       speed,
			StartedAt:   g.Started(),
			State:       g.State,
			NextCheckAt: FirstCheck(speed, now),
		})
		if err != nil {
			s.log.Error("очередь коалиций", "gameID", g.GameID, "err", err)
		}
	}
}

// Run крутится до отмены контекста.
func (s *CoalitionScanner) Run(ctx context.Context) {
	s.log.Info("сбор коалиций запущен", "interval", s.every, "за раз", s.batch)

	ticker := time.NewTicker(scanTick)
	defer ticker.Stop()

	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			s.log.Info("сбор коалиций остановлен")
			return
		case <-ticker.C:
		}
	}
}

func (s *CoalitionScanner) tick(ctx context.Context) {
	on, err := s.settings.CoalitionScanEnabled(ctx)
	if err != nil {
		s.log.Error("выключатель сбора коалиций", "err", err)
		return
	}
	if !on {
		return
	}

	now := s.now()
	due, err := s.store.Due(ctx, now, s.urgentBatch, true)
	if err != nil {
		s.log.Error("очередь коалиций", "err", err)
		return
	}
	if s.lastRegular.IsZero() || now.Sub(s.lastRegular) >= s.every {
		s.lastRegular = now
		regular, err := s.store.Due(ctx, now, s.batch, false)
		if err != nil {
			s.log.Error("очередь коалиций", "err", err)
			return
		}
		due = append(due, regular...)
	}

	seen := map[string]bool{}
	for i, g := range due {
		if ctx.Err() != nil {
			return
		}
		// Срочная партия могла попасть и в обычную выборку.
		if seen[g.GameID] {
			continue
		}
		seen[g.GameID] = true
		// Пауза между партиями, а не перед первой.
		if i > 0 && s.pause > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.pause):
			}
		}
		s.check(ctx, g)
	}
}

// check заходит в одну партию и записывает, что застал.
func (s *CoalitionScanner) check(ctx context.Context, g domain.WatchedGame) {
	now := s.now()

	state, err := s.client.ObserveGame(ctx, g.GameID)
	if err != nil {
		// Партию не бросаем сразу: игра могла лежать. Но и вечно её
		// не тянем — после нескольких неудач подряд считаем безнадёжной.
		done := g.Fails+1 > coalitionRetries
		retry := coalitionRetry
		if g.Urgent {
			retry = coalitionRetryEndgame
		}
		s.log.Warn("сбор коалиций: партия не ответила",
			"gameID", g.GameID, "неудач подряд", g.Fails+1, "бросаем", done, "err", err)
		if err := s.store.Fail(ctx, g.GameID, err.Error(), now, now.Add(retry), done); err != nil {
			s.log.Error("отметка неудачи", "gameID", g.GameID, "err", err)
		}
		return
	}

	teams := CoalitionsOf(state)
	next, done, urgent := planNext(g, state, now)
	if err := s.store.Save(ctx, g.GameID, state.Day, teams, now, next, done, urgent); err != nil {
		s.log.Error("запись коалиций", "gameID", g.GameID, "err", err)
		return
	}

	people := 0
	for _, t := range teams {
		people += len(t.Members)
	}
	s.log.Info("коалиции собраны", "gameID", g.GameID, "партия", g.Title,
		"скорость", g.Speed, "день", state.Day,
		"коалиций", len(teams), "человек", people,
		"эндшпиль", urgent, "кончилась", state.Ended, "следующий", next.Format(time.DateTime))
}

// CoalitionsOf выбирает из состояния партии то, ради чего мы туда ходили.
// Открытая страница партии пополняет архив тем же способом, что и обход,
// поэтому разбор общий.
//
// Коалиции из одного человека пропускаем: архив существует ради пар, а пары
// в одиночке нет. Ботов тоже: номера на сайте у них не бывает, и «сыграть
// вместе» с ними нельзя.
func CoalitionsOf(g *GameState) []domain.Coalition {
	if g == nil || len(g.Teams) == 0 {
		return nil
	}

	members := make(map[int][]domain.CoalitionMember, len(g.Teams))
	for _, p := range g.Players {
		if p.TeamID <= 0 || p.SiteUserID == "" || p.IsAI {
			continue
		}
		if _, ok := g.Teams[p.TeamID]; !ok {
			continue
		}
		members[p.TeamID] = append(members[p.TeamID], domain.CoalitionMember{
			SiteUserID: p.SiteUserID,
			Login:      p.Name,
			Nation:     p.Nation,
		})
	}

	var out []domain.Coalition
	for id, list := range members {
		if len(list) < 2 {
			continue
		}
		out = append(out, domain.Coalition{TeamID: id, Name: g.Teams[id].Name, Members: list})
	}
	return out
}
