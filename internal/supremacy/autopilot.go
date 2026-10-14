package supremacy

// Autopilot — то, что админка делает в партиях сама: жмёт кнопку Мейв
// и шлёт в выбранный город каждую новую армию. Заход на игровой сервер игра засчитывает как вход в партию,
// поэтому ходим редко (по умолчанию раз в 45–50 минут) и только в те партии,
// где это включено руками.

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"Vendetta_admin/internal/domain"
)

// TaskStore — где лежит, в каких партиях что включено, и куда писать итог
// захода. В бою это репозиторий, в тестах — заглушка.
type TaskStore interface {
	AutopilotTasks(ctx context.Context) ([]domain.GameTask, error)
	MarkHeroRun(ctx context.Context, gameID string, at time.Time, result string) error
	MarkRallyRun(ctx context.Context, gameID string, at time.Time, result string, armies []int64) error
}

// autopilotRunner — тот, кто умеет зайти в партию и сделать там дела.
// Интерфейс ради тестов: в бою сюда приходит *Client.
type autopilotRunner interface {
	RunAutopilot(ctx context.Context, gameID string, job AutopilotJob) (*AutopilotRun, error)
}

// Autopilot обходит включённые партии, выжидая между обходами случайную
// паузу от min до max.
type Autopilot struct {
	client autopilotRunner
	store  TaskStore
	log    *slog.Logger
	min    time.Duration
	max    time.Duration

	// now подменяется в тестах; в бою это time.Now.
	now func() time.Time
}

// NewAutopilot: min и max — границы паузы между обходами. Равные значения
// означают ровный интервал, как было раньше; max меньше min считается
// опечаткой и выправляется в равенство, лишь бы воркер не встал совсем.
func NewAutopilot(c autopilotRunner, store TaskStore, min, max time.Duration, log *slog.Logger) *Autopilot {
	if max < min {
		max = min
	}
	return &Autopilot{client: c, store: store, log: log, min: min, max: max, now: time.Now}
}

// wait — сколько ждать до следующего обхода. Пауза каждый раз своя: заход
// в партию игра засчитывает как вход, и ровный ритм в такие часы — это
// подпись робота. Разброс её стирает, ничего не стоя.
func (a *Autopilot) wait() time.Duration {
	if a.max <= a.min {
		return a.min
	}
	return a.min + rand.N(a.max-a.min)
}

// Run крутится до отмены контекста. Первый обход делает сразу: если кнопка
// включена, ждать до первого тика незачем.
func (a *Autopilot) Run(ctx context.Context) {
	a.log.Info("автопилот партий запущен", "пауза от", a.min, "до", a.max)

	// Таймер, а не тикер: пауза у каждого круга своя, и её надо назначать
	// заново после каждого обхода.
	timer := time.NewTimer(a.wait())
	defer timer.Stop()

	for {
		if err := a.tick(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("обход партий", "err", err)
		}
		next := a.wait()
		a.log.Info("следующий обход партий", "через", next)
		timer.Reset(next)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (a *Autopilot) tick(ctx context.Context) error {
	tasks, err := a.store.AutopilotTasks(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.visit(ctx, t)
	}
	return nil
}

// visit заходит в одну партию, делает там всё включённое и записывает,
// чем это кончилось. Итог виден на странице партии, поэтому он нужен
// и при удаче, и при отказе.
func (a *Autopilot) visit(ctx context.Context, t domain.GameTask) {
	job := AutopilotJob{Hero: t.HeroDeploy}
	if t.Rallies() {
		job.RallyTo, job.RallyCity, job.Known = t.RallyProvince, t.RallyCity, t.RallyKnown
	}

	run, err := a.client.RunAutopilot(ctx, t.GameID, job)
	if err != nil {
		// В партию не зашли: отказ один на всё включённое, и записать его
		// надо каждому — иначе страница показывала бы прошлый, удачный итог.
		a.log.Error("заход автопилота", "gameID", t.GameID, "партия", t.Title, "err", err)
		run = &AutopilotRun{Hero: err.Error(), Rally: err.Error()}
	}

	now := a.now()
	if job.Hero {
		if err := a.store.MarkHeroRun(ctx, t.GameID, now, run.Hero); err != nil {
			a.log.Error("запись итога призыва", "gameID", t.GameID, "err", err)
		}
	}
	if job.RallyTo > 0 {
		if err := a.store.MarkRallyRun(ctx, t.GameID, now, run.Rally, run.Armies); err != nil {
			a.log.Error("запись итога сбора", "gameID", t.GameID, "err", err)
		}
	}
}
