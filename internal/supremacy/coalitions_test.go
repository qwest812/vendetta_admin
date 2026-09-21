package supremacy

import (
	"context"
	"errors"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

// Смысл расписания в одном: заглянуть посреди партии, а не в её конце.
// Скоростная живёт около четырёх суток, обычная — два-три месяца, и сроки
// должны попадать в середину, а не за край.
func TestCoalitionSchedule(t *testing.T) {
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		speed float64
		first time.Duration
	}{
		{10, 16 * time.Hour},
		{4, 48 * time.Hour},
		{2, 5 * 24 * time.Hour},
		{1, 30 * 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := FirstCheck(tt.speed, start); !got.Equal(start.Add(tt.first)) {
			t.Errorf("x%.0f: первая проверка через %v, ожидалось %v",
				tt.speed, got.Sub(start), tt.first)
		}
	}
}

// raceState — партия ×4 с порогами 1000 и 1500 и заданными очками.
func raceState(nextDay time.Time, points map[int]int, teams map[int]int) *GameState {
	g := &GameState{
		NextDay: nextDay,
		Players: map[int]Player{},
		Race:    Race{WinPoints: 1000, TeamWinPoints: 1500, Points: points, TeamPoints: teams},
	}
	for id := range points {
		g.Players[id] = Player{ID: id, SiteUserID: "x"}
	}
	return g
}

// До эндшпиля — раз в два игровых дня, без спешки.
func TestPlanNextMidgame(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	st := raceState(now.Add(3*time.Hour), map[int]int{1: 200, 2: 150}, map[int]int{1: 300})

	next, done, urgent := planNext(domain.WatchedGame{Speed: 4}, st, now)
	if done || urgent {
		t.Fatalf("середина партии: done=%v urgent=%v", done, urgent)
	}
	// Игровой день ×4 — шесть часов, два дня — двенадцать.
	if want := now.Add(12 * time.Hour); !next.Equal(want) {
		t.Errorf("следующий заход %s, ждали %s", next, want)
	}
}

// Эндшпиль: приходим за 10 минут до смены дня, а оттуда — за 10 минут
// до следующей, без захода после смены.
func TestPlanNextEndgame(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	day := now.Add(2 * time.Hour)
	st := raceState(day, map[int]int{1: 300}, map[int]int{1: 1000})

	next, done, urgent := planNext(domain.WatchedGame{Speed: 4}, st, now)
	if done || !urgent || !next.Equal(day.Add(-endgameLead)) {
		t.Fatalf("эндшпиль: next=%s done=%v urgent=%v", next, done, urgent)
	}

	// Мы уже перед сменой — следующий раз перед следующей: игровой день
	// ×4 — шесть часов.
	before := day.Add(-endgameLead)
	next, _, urgent = planNext(domain.WatchedGame{Speed: 4}, st, before)
	if !urgent || !next.Equal(day.Add(6*time.Hour-endgameLead)) {
		t.Fatalf("перед сменой: next=%s urgent=%v", next, urgent)
	}
}

// Кончилась — отпускаем.
func TestPlanNextEnded(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	st := raceState(now.Add(time.Hour), map[int]int{1: 900}, nil)
	st.Ended = true
	if _, done, _ := planNext(domain.WatchedGame{Speed: 4}, st, now); !done {
		t.Fatal("кончившуюся партию надо отпускать")
	}
}

// Эндшпиль узнаётся и по сильнейшим вместе: коалиции ещё нет, но пятеро
// лучших уже могут объединиться и взять порог. Боты в эту пятёрку
// не входят — с ними в коалицию не сходятся.
func TestEndgameBySum(t *testing.T) {
	st := raceState(time.Time{}, map[int]int{1: 400, 2: 350, 3: 300, 4: 250, 5: 200, 6: 500}, nil)
	st.Players[6] = Player{ID: 6, IsAI: true}
	if !Endgame(st) {
		t.Error("пятеро живых набирают 1500 — это эндшпиль")
	}
	st.Race.Points[5] = 100
	if Endgame(st) {
		t.Error("пятеро живых набирают 1400 — ещё не эндшпиль")
	}
	// Одиночка на 60% своего порога — тоже эндшпиль.
	st.Race.Points[1] = 600
	if !Endgame(st) {
		t.Error("одиночка на 600 из 1000 — эндшпиль")
	}
}

// Архив существует ради пар. Всё, из чего пары не получится, до базы
// доходить не должно.
func TestCoalitionsOf(t *testing.T) {
	state := &GameState{
		GameID: "10894611",
		Teams: map[int]Team{
			1: {ID: 1, Name: "Союз независимых"},
			2: {ID: 2, Name: "Одиночка"},
		},
		Players: map[int]Player{
			1: {ID: 1, Nation: "Россия", Name: "Stanzinger", SiteUserID: "101753916", TeamID: 1},
			2: {ID: 2, Nation: "Германия", Name: "Vahagn__", SiteUserID: "90265291", TeamID: 1},
			// Бот: номера на сайте у него нет, и «сыграть вместе» с ним нельзя.
			3: {ID: 3, Nation: "Литва", TeamID: 1, IsAI: true},
			// Коалиция из одного человека пары не даёт.
			4: {ID: 4, Nation: "Швеция", SiteUserID: "42", TeamID: 2},
			// Коалиция, которой игра не показала: распущенная.
			5: {ID: 5, Nation: "Италия", SiteUserID: "43", TeamID: 9},
			// Сам по себе.
			6: {ID: 6, Nation: "Египет", SiteUserID: "44"},
		},
	}

	got := CoalitionsOf(state)
	if len(got) != 1 {
		t.Fatalf("коалиций = %d, ожидалась одна: %+v", len(got), got)
	}
	if got[0].TeamID != 1 || got[0].Name != "Союз независимых" {
		t.Errorf("коалиция = %+v", got[0])
	}
	if len(got[0].Members) != 2 {
		t.Fatalf("участников = %d, ожидалось двое (бот не в счёт): %+v",
			len(got[0].Members), got[0].Members)
	}
	for _, m := range got[0].Members {
		if m.SiteUserID == "" {
			t.Errorf("участник без номера на сайте: %+v", m)
		}
	}

	// Партия без коалиций — обычное дело, а не повод для пустой записи.
	if got := CoalitionsOf(&GameState{GameID: "1"}); got != nil {
		t.Errorf("у партии без коалиций получилось %+v", got)
	}
}

// fakeCoalitionStore — очередь и архив в памяти.
type fakeCoalitionStore struct {
	due   []domain.WatchedGame
	saved map[string][]domain.Coalition
	next  map[string]time.Time
	done  map[string]bool
	fails map[string]string
}

func newFakeStore(due ...domain.WatchedGame) *fakeCoalitionStore {
	return &fakeCoalitionStore{
		due:   due,
		saved: map[string][]domain.Coalition{},
		next:  map[string]time.Time{},
		done:  map[string]bool{},
		fails: map[string]string{},
	}
}

func (f *fakeCoalitionStore) Enqueue(context.Context, domain.WatchedGame) error { return nil }

func (f *fakeCoalitionStore) Due(_ context.Context, _ time.Time, limit int, urgent bool) ([]domain.WatchedGame, error) {
	var out []domain.WatchedGame
	for _, g := range f.due {
		if !urgent || g.Urgent {
			out = append(out, g)
		}
	}
	if len(out) > limit {
		return out[:limit], nil
	}
	return out, nil
}

func (f *fakeCoalitionStore) Save(_ context.Context, gameID string, _ int,
	teams []domain.Coalition, _, next time.Time, done, _ bool) error {

	f.saved[gameID] = teams
	f.next[gameID] = next
	f.done[gameID] = done
	return nil
}

func (f *fakeCoalitionStore) Fail(_ context.Context, gameID, reason string, _, next time.Time, done bool) error {
	f.fails[gameID] = reason
	f.next[gameID] = next
	f.done[gameID] = done
	return nil
}

// fakeObserver отдаёт заранее заготовленное состояние.
type fakeObserver struct {
	state *GameState
	err   error
	calls int
}

func (f *fakeObserver) ObserveGame(context.Context, string) (*GameState, error) {
	f.calls++
	return f.state, f.err
}

type fakeSwitch bool

func (s fakeSwitch) CoalitionScanEnabled(context.Context) (bool, error) { return bool(s), nil }

// Выключенный рубильник должен останавливать именно походы в игру,
// а не только запись: иначе «остановил» ничего бы не значило.
func TestScannerRespectsSwitch(t *testing.T) {
	obs := &fakeObserver{state: &GameState{GameID: "1"}}
	store := newFakeStore(domain.WatchedGame{GameID: "1", Speed: 4})

	s := NewCoalitionScanner(obs, store, fakeSwitch(false), time.Minute, quietLog())
	s.tick(context.Background())

	if obs.calls != 0 {
		t.Errorf("при выключенном сборе в игру ходили %d раз", obs.calls)
	}
}

func TestScannerSavesAndCloses(t *testing.T) {
	obs := &fakeObserver{state: &GameState{
		GameID: "10894611",
		Day:    9,
		Teams:  map[int]Team{1: {ID: 1, Name: "Любители"}},
		Players: map[int]Player{
			1: {ID: 1, SiteUserID: "a", TeamID: 1},
			2: {ID: 2, SiteUserID: "b", TeamID: 1},
		},
	}}
	store := newFakeStore(domain.WatchedGame{GameID: "10894611", Speed: 4})

	// every = 0: обычная выборка на каждой проверке, иначе второй tick
	// подряд обычные партии не взял бы — и правильно бы сделал.
	s := NewCoalitionScanner(obs, store, fakeSwitch(true), 0, quietLog())
	s.pause = 0
	s.tick(context.Background())

	if obs.calls != 1 {
		t.Fatalf("заходов в игру %d, ожидался один", obs.calls)
	}
	if teams := store.saved["10894611"]; len(teams) != 1 || len(teams[0].Members) != 2 {
		t.Errorf("записано %+v", teams)
	}
	// Партия идёт — возвращаемся: коалиции соберут под конец.
	if store.done["10894611"] {
		t.Error("идущую партию отпустили после первой проверки")
	}

	// Кончилась — отпускаем.
	obs.state.Ended = true
	s.tick(context.Background())
	if !store.done["10894611"] {
		t.Error("кончившуюся партию не отпустили")
	}
}

// Одна партия в обеих выборках (срочной и обычной) — один заход.
func TestScannerUrgentOnce(t *testing.T) {
	obs := &fakeObserver{state: &GameState{GameID: "1"}}
	store := newFakeStore(domain.WatchedGame{GameID: "1", Speed: 4, Urgent: true})

	s := NewCoalitionScanner(obs, store, fakeSwitch(true), time.Minute, quietLog())
	s.pause = 0
	s.tick(context.Background())
	if obs.calls != 1 {
		t.Fatalf("заходов %d, ожидался один", obs.calls)
	}
}

// Молчание игры — не повод бросать партию сразу: она лежит минутами.
// Но и вечно тянуть её нельзя.
func TestScannerRetriesThenGivesUp(t *testing.T) {
	obs := &fakeObserver{err: errors.New("игра не отвечает")}
	store := newFakeStore(domain.WatchedGame{GameID: "7", Speed: 4, Fails: 0})

	s := NewCoalitionScanner(obs, store, fakeSwitch(true), 0, quietLog())
	s.tick(context.Background())
	if store.done["7"] {
		t.Error("партию бросили с первой неудачи")
	}

	store.due = []domain.WatchedGame{{GameID: "7", Speed: 4, Fails: 2}}
	s.tick(context.Background())
	if !store.done["7"] {
		t.Error("после трёх неудач партию пора бросать")
	}
	if store.fails["7"] == "" {
		t.Error("причина неудачи не записана")
	}
}
