package supremacy

// Autopilot — то, что админка делает в партиях сама: сейчас это одна
// кнопка Мейв. Заход на игровой сервер игра засчитывает как вход в партию,
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
	HeroDeployEnabled(ctx context.Context) ([]domain.GameTask, error)
	MarkHeroRun(ctx context.Context, gameID string, at time.Time, result string) error
}

// heroDeployer — тот, кто умеет нажать кнопку. Интерфейс ради тестов:
// в бою сюда приходит *Client.
type heroDeployer interface {
	DeployInfantry(ctx context.Context, gameID string) (string, error)
}

// Autopilot обходит включённые партии, выжидая между обходами случайную
// паузу от min до max.
type Autopilot struct {
	client heroDeployer
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
func NewAutopilot(c heroDeployer, store TaskStore, min, max time.Duration, log *slog.Logger) *Autopilot {
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
	tasks, err := a.store.HeroDeployEnabled(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.deploy(ctx, t)
	}
	return nil
}

// deploy жмёт кнопку в одной партии и записывает, чем это кончилось.
// Итог виден на странице партии, поэтому он нужен и при удаче, и при отказе.
func (a *Autopilot) deploy(ctx context.Context, t domain.GameTask) {
	result, err := a.client.DeployInfantry(ctx, t.GameID)
	if err != nil {
		// Отказ игры записываем словами: по нему потом и разбираются.
		// Чаще всего это «героиня уже размещается» — то есть всё в порядке,
		// просто рано.
		result = err.Error()
		a.log.Error("призыв пехоты", "gameID", t.GameID, "партия", t.Title, "err", err)
	}

	if err := a.store.MarkHeroRun(ctx, t.GameID, a.now(), result); err != nil {
		a.log.Error("запись итога захода", "gameID", t.GameID, "err", err)
	}
}
