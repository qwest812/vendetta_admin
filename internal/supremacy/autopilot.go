package supremacy

// Autopilot — то, что админка делает в партиях сама: жмёт кнопку Мейв
// и шлёт в выбранный город каждую новую армию. Заход на игровой сервер игра засчитывает как вход в партию,
// поэтому ходим редко и только в те партии, где это включено руками.
//
// Пауза у каждой партии своя и зависит от её скорости: настройка (по
// умолчанию 45–50 минут) подобрана под x4, и в партии x1 игровой день
// идёт вчетверо дольше — туда ходим вчетверо реже, в x10 — чаще.

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
	// GameSpeed — скорость партии; 0 — не узнали.
	GameSpeed(ctx context.Context, gameID string) float64
}

// Настройка паузы подобрана под эту скорость.
const autopilotBaseSpeed = 4

// autopilotFloor — чаще этого в партию не ходим, какой бы быстрой она ни
// была: заход засчитывается как вход, и частить нельзя.
const autopilotFloor = 5 * time.Minute

// autopilotTick — как часто воркер сверяется с расписанием партий. Это
// взгляд в свою память и базу, а не в игру.
const autopilotTick = time.Minute

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
	// next — когда в какую партию идти в следующий раз. Нет записи —
	// идём на ближайшей сверке: партию только что включили.
	next map[string]time.Time
}

// NewAutopilot: min и max — границы паузы между обходами. Равные значения
// означают ровный интервал, как было раньше; max меньше min считается
// опечаткой и выправляется в равенство, лишь бы воркер не встал совсем.
func NewAutopilot(c autopilotRunner, store TaskStore, min, max time.Duration, log *slog.Logger) *Autopilot {
	if max < min {
		max = min
	}
	return &Autopilot{client: c, store: store, log: log, min: min, max: max, now: time.Now,
		next: map[string]time.Time{}}
}

// waitFor — пауза для партии этой скорости: случайная из настройки,
// растянутая или сжатая под скорость. Скорость не узнали — считаем x4,
// как и было до учёта скорости.
func (a *Autopilot) waitFor(speed float64) time.Duration {
	return AutopilotPause(a.wait(), speed)
}

// AutopilotPause растягивает паузу из настройки (она для x4) под скорость
// партии. Общая для воркера и страницы партии, которая её показывает.
func AutopilotPause(base time.Duration, speed float64) time.Duration {
	if speed <= 0 {
		speed = autopilotBaseSpeed
	}
	d := time.Duration(float64(base) * autopilotBaseSpeed / speed)
	if d < autopilotFloor {
		d = autopilotFloor
	}
	return d
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

// Run крутится до отмены контекста. Первая сверка — сразу: включённые
// партии без срока идут в работу, ждать до первого тика незачем.
func (a *Autopilot) Run(ctx context.Context) {
	a.log.Info("автопилот партий запущен", "пауза для x4 от", a.min, "до", a.max)

	ticker := time.NewTicker(autopilotTick)
	defer ticker.Stop()
	for {
		if err := a.tick(ctx); err != nil && ctx.Err() == nil {
			a.log.Error("обход партий", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick заходит в партии, чей срок подошёл, и назначает им следующий —
// по их скорости.
func (a *Autopilot) tick(ctx context.Context) error {
	tasks, err := a.store.AutopilotTasks(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		live[t.GameID] = true
		if at, ok := a.next[t.GameID]; ok && a.now().Before(at) {
			continue
		}
		a.visit(ctx, t)
		speed := a.client.GameSpeed(ctx, t.GameID)
		wait := a.waitFor(speed)
		a.next[t.GameID] = a.now().Add(wait)
		a.log.Info("следующий заход автопилота", "gameID", t.GameID, "скорость", speed, "через", wait)
	}
	// Выключенные партии забываем: включат снова — пойдём сразу.
	for id := range a.next {
		if !live[id] {
			delete(a.next, id)
		}
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
