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
	"time"

	"Vendetta_admin/internal/domain"
)

// CoalitionStore — где живёт очередь обхода и то, что в ней нашлось.
type CoalitionStore interface {
	Enqueue(ctx context.Context, g domain.WatchedGame) error
	Due(ctx context.Context, at time.Time, limit int) ([]domain.WatchedGame, error)
	Save(ctx context.Context, gameID string, day int, teams []domain.Coalition, at, next time.Time, done bool) error
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

// coalitionPlan — расписание обхода для партий не медленнее minSpeed.
// Списком, а не формулой: сроки взяты из наблюдений за живыми партиями,
// а не выведены из чего-то, и править их будут тоже по наблюдениям.
type coalitionPlan struct {
	minSpeed  float64
	first     time.Duration
	repeat    time.Duration
	maxChecks int
}

// Замеренная жизнь партий: x10 — около 34 часов, x4 — около четырёх суток,
// x1 — два-три месяца. Заходим примерно на середине.
var coalitionPlans = []coalitionPlan{
	{minSpeed: 8, first: 16 * time.Hour, maxChecks: 1},
	{minSpeed: 3, first: 48 * time.Hour, maxChecks: 1},
	{minSpeed: 1.5, first: 5 * 24 * time.Hour, maxChecks: 1},
	// Обычная партия живёт достаточно, чтобы взглянуть несколько раз:
	// союзы в ней складываются и распадаются неспешно.
	{minSpeed: 0, first: 30 * 24 * time.Hour, repeat: 30 * 24 * time.Hour, maxChecks: 4},
}

// coalitionRetry — через сколько повторить заход, если игра не ответила.
// Она лежит минутами, а не сутками, но и торопиться нам некуда.
const coalitionRetry = 2 * time.Hour

// coalitionRetries — сколько неудач терпим сверх плана, прежде чем бросить
// партию. Без этого потолка мёртвая партия висела бы в очереди вечно.
const coalitionRetries = 2

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

// nextCheck — когда возвращаться после удачного захода и стоит ли вообще.
func nextCheck(speed float64, checks int, from time.Time) (time.Time, bool) {
	p := planFor(speed)
	if checks >= p.maxChecks || p.repeat == 0 {
		return from, true
	}
	return from.Add(p.repeat), false
}

// CoalitionScanner раз в every берёт партии, которым пора, и записывает
// их коалиции.
type CoalitionScanner struct {
	client   gameObserver
	store    CoalitionStore
	settings CoalitionSwitch
	log      *slog.Logger

	every time.Duration
	// batch — сколько партий берём за тик. Обход намеренно неспешный:
	// состояние партии весит полтора мегабайта, и торопиться нам некуда.
	batch int
	// pause — сколько ждём между партиями внутри тика.
	pause time.Duration

	now func() time.Time
}

func NewCoalitionScanner(c gameObserver, store CoalitionStore, settings CoalitionSwitch,
	every time.Duration, log *slog.Logger) *CoalitionScanner {

	return &CoalitionScanner{
		client: c, store: store, settings: settings, log: log,
		every: every, batch: 5, pause: 3 * time.Second, now: time.Now,
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

	ticker := time.NewTicker(s.every)
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

	due, err := s.store.Due(ctx, s.now(), s.batch)
	if err != nil {
		s.log.Error("очередь коалиций", "err", err)
		return
	}
	for i, g := range due {
		if ctx.Err() != nil {
			return
		}
		// Пауза между партиями, а не перед первой: тик и так редкий.
		if i > 0 {
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
		// не тянем — после нескольких неудач считаем безнадёжной.
		done := g.Checks+1 >= planFor(g.Speed).maxChecks+coalitionRetries
		s.log.Warn("сбор коалиций: партия не ответила",
			"gameID", g.GameID, "попыток", g.Checks+1, "бросаем", done, "err", err)
		if err := s.store.Fail(ctx, g.GameID, err.Error(), now, now.Add(coalitionRetry), done); err != nil {
			s.log.Error("отметка неудачи", "gameID", g.GameID, "err", err)
		}
		return
	}

	teams := CoalitionsOf(state)
	next, done := nextCheck(g.Speed, g.Checks+1, now)
	if err := s.store.Save(ctx, g.GameID, state.Day, teams, now, next, done); err != nil {
		s.log.Error("запись коалиций", "gameID", g.GameID, "err", err)
		return
	}

	people := 0
	for _, t := range teams {
		people += len(t.Members)
	}
	s.log.Info("коалиции собраны", "gameID", g.GameID, "партия", g.Title,
		"скорость", g.Speed, "день", state.Day,
		"коалиций", len(teams), "человек", people, "ещё зайдём", !done)
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
