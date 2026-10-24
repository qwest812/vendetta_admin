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
	rallies map[string]string
	armies  map[string][]int64
	listErr error
}

func (f *fakeTasks) AutopilotTasks(context.Context) ([]domain.GameTask, error) {
	return f.enabled, f.listErr
}

func (f *fakeTasks) MarkRallyRun(_ context.Context, gameID string, _ time.Time, result string, armies []int64) error {
	if f.rallies == nil {
		f.rallies, f.armies = map[string]string{}, map[string][]int64{}
	}
	f.rallies[gameID], f.armies[gameID] = result, armies
	return nil
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
	rally   string
	armies  []int64
	err     error
	visited []string
	jobs    []AutopilotJob
	// speeds — скорость партии; нет записи — не узнали (0).
	speeds map[string]float64
}

func (f *fakeDeployer) GameSpeed(_ context.Context, gameID string) float64 { return f.speeds[gameID] }

func (f *fakeDeployer) RunAutopilot(_ context.Context, gameID string, job AutopilotJob) (*AutopilotRun, error) {
	f.visited = append(f.visited, gameID)
	f.jobs = append(f.jobs, job)
	if f.err != nil {
		return nil, f.err
	}
	return &AutopilotRun{Hero: f.result, Rally: f.rally, Armies: f.armies}, nil
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Автопилот ходит только в те партии, которые ему назвали, и по каждой
// оставляет след: страница партии показывает именно его.
func TestAutopilotVisitsEnabledGames(t *testing.T) {
	store := &fakeTasks{enabled: []domain.GameTask{
		{GameID: "1", Title: "первая", HeroDeploy: true}, {GameID: "2", Title: "вторая", HeroDeploy: true},
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
	store := &fakeTasks{enabled: []domain.GameTask{{GameID: "7", HeroDeploy: true, RallyAuto: true, RallyProvince: 5}}}
	deployer := &fakeDeployer{err: errors.New("resultCode=-17 session expired")}
	a := NewAutopilot(deployer, store, time.Hour, time.Hour, quietLog())

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if got := store.runs["7"]; got != "resultCode=-17 session expired" {
		t.Errorf("итог = %q", got)
	}
	// Не зашли — отказ один на всё включённое, а список армий не трогаем:
	// мы их не видели.
	if got := store.rallies["7"]; got != "resultCode=-17 session expired" {
		t.Errorf("итог сбора = %q", got)
	}
	if store.armies["7"] != nil {
		t.Errorf("список армий затёрт: %v", store.armies["7"])
	}
}

// Сбор и Мейв ходят одним заходом: автопилот отдаёт клиенту и то, и другое,
// а итоги пишет каждый в своё поле. Сбор без города не включается.
func TestAutopilotRalliesInSameVisit(t *testing.T) {
	store := &fakeTasks{enabled: []domain.GameTask{
		{GameID: "1", HeroDeploy: true, RallyAuto: true, RallyProvince: 135, RallyCity: "Город",
			RallyKnown: []int64{10}},
		{GameID: "2", HeroDeploy: true, RallyAuto: true},
	}}
	deployer := &fakeDeployer{result: "призвала", rally: "отправлено: 1", armies: []int64{10, 11}}
	a := NewAutopilot(deployer, store, time.Hour, time.Hour, quietLog())

	if err := a.tick(context.Background()); err != nil {
		t.Fatalf("обход: %v", err)
	}
	if len(deployer.visited) != 2 {
		t.Fatalf("заходов = %v, ожидалось по одному на партию", deployer.visited)
	}
	job := deployer.jobs[0]
	if !job.Hero || job.RallyTo != 135 || job.RallyCity != "Город" || len(job.Known) != 1 {
		t.Errorf("задание первой партии = %+v", job)
	}
	if store.runs["1"] != "призвала" || store.rallies["1"] != "отправлено: 1" || len(store.armies["1"]) != 2 {
		t.Errorf("итоги первой партии: %q, %q, %v", store.runs["1"], store.rallies["1"], store.armies["1"])
	}
	if deployer.jobs[1].RallyTo != 0 {
		t.Errorf("сбор без города ушёл в игру: %+v", deployer.jobs[1])
	}
	if _, ok := store.rallies["2"]; ok {
		t.Error("итог сбора записан партии, где сбор не шёл")
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

// Пауза растягивается под скорость: настройка — для x4, в x1 вчетверо
// дольше, в x10 короче, но не чаще пяти минут. Скорость не узнали —
// считаем x4.
func TestAutopilotWaitFollowsSpeed(t *testing.T) {
	a := NewAutopilot(&fakeDeployer{}, &fakeTasks{runs: map[string]string{}}, 45*time.Minute, 45*time.Minute, quietLog())
	for speed, want := range map[float64]time.Duration{
		4:   45 * time.Minute,
		1:   180 * time.Minute,
		10:  18 * time.Minute,
		0:   45 * time.Minute,
		100: autopilotFloor,
	} {
		if got := a.waitFor(speed); got != want {
			t.Errorf("x%v: %v, ожидали %v", speed, got, want)
		}
	}
}

// Срок у каждой партии свой: быструю обходим снова, пока медленная ждёт.
func TestAutopilotSchedulesPerGame(t *testing.T) {
	store := &fakeTasks{enabled: []domain.GameTask{
		{GameID: "fast", HeroDeploy: true}, {GameID: "slow", HeroDeploy: true},
	}}
	dep := &fakeDeployer{speeds: map[string]float64{"fast": 10, "slow": 1}}
	a := NewAutopilot(dep, store, 45*time.Minute, 45*time.Minute, quietLog())
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	if err := a.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(dep.visited) != 2 {
		t.Fatalf("первая сверка: %v", dep.visited)
	}
	now = now.Add(20 * time.Minute) // быстрой пора (18 мин), медленной — нет (3 ч)
	if err := a.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(dep.visited) != 3 || dep.visited[2] != "fast" {
		t.Errorf("через 20 минут: %v", dep.visited)
	}
}

// После перезапуска срок считается от последнего захода из базы: недавно
// заходили — ждём, давно или ни разу — идём сразу.
func TestAutopilotResumesFromLastRun(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	store := &fakeTasks{enabled: []domain.GameTask{
		{GameID: "recent", HeroDeploy: true, LastRunAt: now.Add(-10 * time.Minute)},
		{GameID: "stale", HeroDeploy: true, LastRunAt: now.Add(-2 * time.Hour)},
		{GameID: "never", HeroDeploy: true},
	}}
	dep := &fakeDeployer{speeds: map[string]float64{"recent": 4, "stale": 4, "never": 4}}
	a := NewAutopilot(dep, store, 45*time.Minute, 45*time.Minute, quietLog())
	a.now = func() time.Time { return now }
	if err := a.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(dep.visited) != 2 || dep.visited[0] != "stale" || dep.visited[1] != "never" {
		t.Errorf("после перезапуска зашли в %v, ожидали stale и never", dep.visited)
	}
}
