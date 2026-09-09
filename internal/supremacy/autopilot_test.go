package supremacy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

type fakeTasks struct {
	enabled []domain.GameTask
	runs    map[string]string
	listErr error
}

func (f *fakeTasks) HeroDeployEnabled(context.Context) ([]domain.GameTask, error) {
	return f.enabled, f.listErr
}

func (f *fakeTasks) MarkHeroRun(_ context.Context, gameID string, _ time.Time, result string) error {
	if f.runs == nil {
		f.runs = map[string]string{}
	}
	f.runs[gameID] = result
	return nil
}

type fakeDeployer struct {
	result  string
	err     error
	visited []string
}

func (f *fakeDeployer) DeployInfantry(_ context.Context, gameID string) (string, error) {
	f.visited = append(f.visited, gameID)
	return f.result, f.err
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Автопилот ходит только в те партии, которые ему назвали, и по каждой
// оставляет след: страница партии показывает именно его.
func TestAutopilotVisitsEnabledGames(t *testing.T) {
	store := &fakeTasks{enabled: []domain.GameTask{
		{GameID: "1", Title: "первая"}, {GameID: "2", Title: "вторая"},
	}}
	deployer := &fakeDeployer{result: "Мейв призвала пехоту"}
	a := NewAutopilot(deployer, store, time.Hour, time.Hour, quietLog())

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if len(deployer.visited) != 2 {
		t.Fatalf("заходов = %v, ожидалось два", deployer.visited)
	}
	for _, id := range []string{"1", "2"} {
		if got := store.runs[id]; got != "Мейв призвала пехоту" {
			t.Errorf("итог по партии %s = %q", id, got)
		}
	}
}

// Отказ игры записывается как есть: по нему потом и разбираются.
func TestAutopilotRecordsGameError(t *testing.T) {
	store := &fakeTasks{enabled: []domain.GameTask{{GameID: "7"}}}
	deployer := &fakeDeployer{err: errors.New("resultCode=-17 session expired")}
	a := NewAutopilot(deployer, store, time.Hour, time.Hour, quietLog())

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if got := store.runs["7"]; got != "resultCode=-17 session expired" {
		t.Errorf("итог = %q", got)
	}
}

// Без включённых партий автопилот в игру не ходит: заход туда игра
// засчитывает как вход в партию.
func TestAutopilotDoesNothingWhenDisabled(t *testing.T) {
	deployer := &fakeDeployer{}
	a := NewAutopilot(deployer, &fakeTasks{}, time.Hour, time.Hour, quietLog())

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if len(deployer.visited) != 0 {
		t.Errorf("лишние заходы: %v", deployer.visited)
	}
}

// Ошибка базы не должна разбираться как «партий нет»: обход просто не
// состоялся, и об этом надо сказать вызывающему.
func TestAutopilotReportsStoreError(t *testing.T) {
	a := NewAutopilot(&fakeDeployer{}, &fakeTasks{listErr: errors.New("база недоступна")},
		time.Hour, time.Hour, quietLog())
	if err := a.tick(context.Background()); err == nil {
		t.Error("ожидалась ошибка обхода")
	}
}

// Пауза между обходами каждый раз своя: ровный ритм захода в партию — это
// подпись робота, и разброс её стирает. Границы при этом соблюдаются строго:
// нижняя — чтобы не частить с тем, что игра считает входом в партию.
func TestAutopilotWaitStaysInRange(t *testing.T) {
	const from, to = 45 * time.Minute, 50 * time.Minute
	a := NewAutopilot(&fakeDeployer{}, &fakeTasks{}, from, to, quietLog())

	seen := map[time.Duration]bool{}
	for range 200 {
		d := a.wait()
		if d < from || d >= to {
			t.Fatalf("пауза %s вне границ %s..%s", d, from, to)
		}
		seen[d] = true
	}
	// Одно и то же значение двести раз означало бы, что разброса нет.
	if len(seen) < 2 {
		t.Errorf("пауза не меняется: получено %d разных значений", len(seen))
	}
}

// Равные границы — это ровный интервал, как было до разброса. Перевёрнутые
// границы считаем опечаткой и не даём воркеру встать совсем.
func TestAutopilotWaitWithoutSpread(t *testing.T) {
	a := NewAutopilot(&fakeDeployer{}, &fakeTasks{}, time.Hour, time.Hour, quietLog())
	if d := a.wait(); d != time.Hour {
		t.Errorf("пауза = %s, ожидался ровный час", d)
	}

	back := NewAutopilot(&fakeDeployer{}, &fakeTasks{}, time.Hour, time.Minute, quietLog())
	if d := back.wait(); d != time.Hour {
		t.Errorf("при перевёрнутых границах пауза = %s, ожидался час", d)
	}
}
