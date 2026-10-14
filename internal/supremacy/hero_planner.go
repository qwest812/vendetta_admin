package supremacy

// HeroPlanner исполняет приказы на размещение героев. Раз в минуту он
// спрашивает только свою базу, а в партию ходит, когда у какого-то приказа
// подошёл срок: к началу нужного дня или к повторной проверке. Заход игра
// засчитывает как вход в партию, поэтому за один заход разбираются все
// приказы партии сразу.

import (
	"context"
	"log/slog"
	"time"

	"Vendetta_admin/internal/domain"
)

const heroPlanTick = time.Minute

// HeroPlanStore — где лежат приказы. В бою это репозиторий.
type HeroPlanStore interface {
	Due(ctx context.Context, now time.Time) ([]domain.HeroPlan, error)
	Open(ctx context.Context, gameID string) ([]domain.HeroPlan, error)
	Mark(ctx context.Context, id int64, at time.Time, result string, next time.Time) error
}

type heroOrderRunner interface {
	RunHeroOrders(ctx context.Context, gameID string, orders []domain.HeroPlan, now time.Time) ([]HeroOrderRun, error)
}

type HeroPlanner struct {
	client heroOrderRunner
	store  HeroPlanStore
	log    *slog.Logger
	now    func() time.Time
}

func NewHeroPlanner(c heroOrderRunner, store HeroPlanStore, log *slog.Logger) *HeroPlanner {
	return &HeroPlanner{client: c, store: store, log: log, now: time.Now}
}

func (h *HeroPlanner) Run(ctx context.Context) {
	h.log.Info("приказы на размещение героев запущены", "проверка", heroPlanTick)
	ticker := time.NewTicker(heroPlanTick)
	defer ticker.Stop()
	for {
		if err := h.tick(ctx); err != nil && ctx.Err() == nil {
			h.log.Error("обход приказов героев", "err", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (h *HeroPlanner) tick(ctx context.Context) error {
	due, err := h.store.Due(ctx, h.now())
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if seen[p.GameID] {
			continue
		}
		seen[p.GameID] = true
		if err := h.visit(ctx, p.GameID); err != nil {
			h.log.Error("приказы героев", "gameID", p.GameID, "err", err)
		}
	}
	return nil
}

// visit разбирает все открытые приказы партии одним заходом. Раз уж
// зашли, проверяем и те, чей срок не подошёл: место в лимите или мораль
// могли появиться раньше, чем мы собирались заглянуть.
func (h *HeroPlanner) visit(ctx context.Context, gameID string) error {
	orders, err := h.store.Open(ctx, gameID)
	if err != nil || len(orders) == 0 {
		return err
	}
	now := h.now()
	runs, err := h.client.RunHeroOrders(ctx, gameID, orders, now)
	if err != nil {
		// Не зашли — приказы не теряются, а ждут часа.
		runs = make([]HeroOrderRun, len(orders))
		for i, o := range orders {
			runs[i] = HeroOrderRun{ID: o.ID, Result: err.Error(), Next: now.Add(heroRetry)}
		}
	}
	for _, run := range runs {
		h.log.Info("приказ героя", "gameID", gameID, "приказ", run.ID, "итог", run.Result, "следующий заход", run.Next)
		if err := h.store.Mark(ctx, run.ID, now, run.Result, run.Next); err != nil {
			h.log.Error("запись итога приказа героя", "gameID", gameID, "err", err)
		}
	}
	return nil
}
