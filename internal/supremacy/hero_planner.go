package supremacy

// HeroPlanner исполняет отложенные размещения героев. Раз в минуту он
// спрашивает только свою базу, а в партию ходит к сроку плана: к началу
// нужного дня или к повтору после отказа. Заход игра засчитывает как
// вход в партию, поэтому лишних не делаем.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"Vendetta_admin/internal/domain"
)

const (
	heroPlanTick = time.Minute
	// heroPlanAttempts — сколько заходов даём плану. Ожидание дня — это
	// один-два захода, а повторы после отказа идут раз в час: двое суток
	// попыток хватает, чтобы мораль выросла или бой кончился, а дальше
	// упорствовать нет смысла — что-то не так, и решать человеку.
	heroPlanAttempts = 48
)

// HeroPlanStore — где лежат планы. В бою это репозиторий.
type HeroPlanStore interface {
	Due(ctx context.Context, now time.Time) ([]domain.HeroPlan, error)
	Mark(ctx context.Context, id int64, at time.Time, result string, next time.Time) error
}

type heroPlanRunner interface {
	RunHeroPlan(ctx context.Context, p domain.HeroPlan, now time.Time) (*HeroPlanRun, error)
}

type HeroPlanner struct {
	client heroPlanRunner
	store  HeroPlanStore
	log    *slog.Logger
	now    func() time.Time
}

func NewHeroPlanner(c heroPlanRunner, store HeroPlanStore, log *slog.Logger) *HeroPlanner {
	return &HeroPlanner{client: c, store: store, log: log, now: time.Now}
}

func (h *HeroPlanner) Run(ctx context.Context) {
	h.log.Info("размещение героев по плану запущено", "проверка", heroPlanTick)
	ticker := time.NewTicker(heroPlanTick)
	defer ticker.Stop()
	for {
		if err := h.tick(ctx); err != nil && ctx.Err() == nil {
			h.log.Error("обход планов героев", "err", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (h *HeroPlanner) tick(ctx context.Context) error {
	plans, err := h.store.Due(ctx, h.now())
	if err != nil {
		return err
	}
	for _, p := range plans {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h.visit(ctx, p)
	}
	return nil
}

// visit исполняет один план и записывает итог. Не зашли в партию —
// пробуем через час, как и при отказе игры.
func (h *HeroPlanner) visit(ctx context.Context, p domain.HeroPlan) {
	now := h.now()
	run, err := h.client.RunHeroPlan(ctx, p, now)
	if err != nil {
		h.log.Error("план героя", "gameID", p.GameID, "герой", p.Hero, "err", err)
		run = &HeroPlanRun{Result: err.Error(), Next: now.Add(heroRetry)}
	}
	if !run.Next.IsZero() && p.Attempts+1 >= heroPlanAttempts {
		run.Result = fmt.Sprintf("сдались после %d заходов: %s", p.Attempts+1, run.Result)
		run.Next = time.Time{}
	}
	h.log.Info("план героя", "gameID", p.GameID, "герой", p.Hero, "итог", run.Result, "следующий заход", run.Next)
	if err := h.store.Mark(ctx, p.ID, now, run.Result, run.Next); err != nil {
		h.log.Error("запись итога плана героя", "gameID", p.GameID, "err", err)
	}
}
