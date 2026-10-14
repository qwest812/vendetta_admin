package supremacy

import (
	"context"
	"errors"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

type fakePlans struct {
	due, open []domain.HeroPlan
	marks     map[int64]time.Time
}

func (f *fakePlans) Due(context.Context, time.Time) ([]domain.HeroPlan, error) { return f.due, nil }
func (f *fakePlans) Open(_ context.Context, gameID string) ([]domain.HeroPlan, error) {
	var out []domain.HeroPlan
	for _, p := range f.open {
		if p.GameID == gameID {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakePlans) Mark(_ context.Context, id int64, _ time.Time, _ string, next time.Time) error {
	if f.marks == nil {
		f.marks = map[int64]time.Time{}
	}
	f.marks[id] = next
	return nil
}

type fakeOrderRunner struct {
	err    error
	visits []string
}

func (f *fakeOrderRunner) RunHeroOrders(_ context.Context, gameID string, orders []domain.HeroPlan, _ time.Time) ([]HeroOrderRun, error) {
	f.visits = append(f.visits, gameID)
	return nil, f.err
}

// Заход один на партию, сколько бы её приказов ни подошло, и разбирает
// он все открытые приказы. Не зашли — приказы ждут часа, а не теряются.
func TestHeroPlannerOneVisitPerGame(t *testing.T) {
	now := time.Date(2026, 10, 14, 23, 0, 0, 0, time.UTC)
	store := &fakePlans{
		due:  []domain.HeroPlan{{ID: 1, GameID: "a"}, {ID: 2, GameID: "a"}},
		open: []domain.HeroPlan{{ID: 1, GameID: "a"}, {ID: 2, GameID: "a"}, {ID: 3, GameID: "a"}},
	}
	runner := &fakeOrderRunner{err: errors.New("сервер молчит")}
	h := NewHeroPlanner(runner, store, quietLog())
	h.now = func() time.Time { return now }

	if err := h.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if len(runner.visits) != 1 {
		t.Errorf("заходов = %v, ожидался один", runner.visits)
	}
	for _, id := range []int64{1, 2, 3} {
		if got := store.marks[id]; !got.Equal(now.Add(heroRetry)) {
			t.Errorf("приказ %d: следующий заход %v, ожидался через час", id, got)
		}
	}
}

// Смена дня считается от ближайшей: день 8 при идущем 5-м и смене
// в 12:00 при часовом дне ускоренной партии — это 14:00.
func TestDayStart(t *testing.T) {
	next := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	g := &GameState{Day: 5, NextDay: next, DayLength: time.Hour}
	if got := g.DayStart(8); !got.Equal(next.Add(2 * time.Hour)) {
		t.Errorf("начало 8-го дня = %v", got)
	}
	if !g.DayStart(5).IsZero() {
		t.Error("наступивший день получил срок в будущем")
	}
}
