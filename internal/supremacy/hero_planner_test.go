package supremacy

import (
	"context"
	"errors"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

type fakePlans struct {
	due   []domain.HeroPlan
	marks map[int64]time.Time
	res   map[int64]string
}

func (f *fakePlans) Due(context.Context, time.Time) ([]domain.HeroPlan, error) { return f.due, nil }
func (f *fakePlans) Mark(_ context.Context, id int64, _ time.Time, result string, next time.Time) error {
	if f.marks == nil {
		f.marks, f.res = map[int64]time.Time{}, map[int64]string{}
	}
	f.marks[id], f.res[id] = next, result
	return nil
}

type fakePlanRunner struct {
	run *HeroPlanRun
	err error
}

func (f fakePlanRunner) RunHeroPlan(context.Context, domain.HeroPlan, time.Time) (*HeroPlanRun, error) {
	return f.run, f.err
}

// Не зашли в партию — план не теряется, а ждёт часа. Исчерпав попытки,
// план закрывается со словами, а не висит вечно.
func TestHeroPlannerRetriesAndGivesUp(t *testing.T) {
	now := time.Date(2026, 10, 14, 23, 0, 0, 0, time.UTC)
	store := &fakePlans{due: []domain.HeroPlan{{ID: 1}, {ID: 2, Attempts: heroPlanAttempts - 1}}}
	h := NewHeroPlanner(fakePlanRunner{err: errors.New("сервер молчит")}, store, quietLog())
	h.now = func() time.Time { return now }

	if err := h.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if got := store.marks[1]; !got.Equal(now.Add(heroRetry)) {
		t.Errorf("следующий заход = %v, ожидался через час", got)
	}
	if !store.marks[2].IsZero() || store.res[2] == "" {
		t.Errorf("план после всех попыток не закрыт: %v, %q", store.marks[2], store.res[2])
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
