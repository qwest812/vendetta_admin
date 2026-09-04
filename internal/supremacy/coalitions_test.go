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

	// Быстрой партии хватает одного захода: второй пришёлся бы уже
	// на завершённую партию, где коалиций может не остаться вовсе.
	if _, done := nextCheck(4, 1, start); !done {
		t.Error("после первой проверки скоростную партию надо отпускать")
	}
	// Обычную смотрим несколько раз: союзы в ней складываются неспешно.
	next, done := nextCheck(1, 1, start)
	if done {
		t.Error("обычную партию рано отпускать после первой проверки")
	}
	if want := start.Add(30 * 24 * time.Hour); !next.Equal(want) {
		t.Errorf("следующая проверка обычной партии %v, ожидалось %v", next, want)
	}
	if _, done := nextCheck(1, 4, start); !done {
		t.Error("после четвёртой проверки обычную партию надо отпускать")
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

func (f *fakeCoalitionStore) Due(_ context.Context, _ time.Time, limit int) ([]domain.WatchedGame, error) {
	if len(f.due) > limit {
		return f.due[:limit], nil
	}
	return f.due, nil
}

func (f *fakeCoalitionStore) Save(_ context.Context, gameID string, _ int,
	teams []domain.Coalition, _, next time.Time, done bool) error {

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

	s := NewCoalitionScanner(obs, store, fakeSwitch(true), time.Minute, quietLog())
	s.pause = 0
	s.tick(context.Background())

	if obs.calls != 1 {
		t.Fatalf("заходов в игру %d, ожидался один", obs.calls)
	}
	if teams := store.saved["10894611"]; len(teams) != 1 || len(teams[0].Members) != 2 {
		t.Errorf("записано %+v", teams)
	}
	// Скоростная партия своё отдала: возвращаться в неё незачем.
	if !store.done["10894611"] {
		t.Error("скоростную партию не отпустили после первой проверки")
	}
}

// Молчание игры — не повод бросать партию сразу: она лежит минутами.
// Но и вечно тянуть её нельзя.
func TestScannerRetriesThenGivesUp(t *testing.T) {
	obs := &fakeObserver{err: errors.New("игра не отвечает")}
	store := newFakeStore(domain.WatchedGame{GameID: "7", Speed: 4, Checks: 0})

	s := NewCoalitionScanner(obs, store, fakeSwitch(true), time.Minute, quietLog())
	s.tick(context.Background())
	if store.done["7"] {
		t.Error("партию бросили с первой неудачи")
	}

	store.due = []domain.WatchedGame{{GameID: "7", Speed: 4, Checks: 2}}
	s.tick(context.Background())
	if !store.done["7"] {
		t.Error("после трёх неудач партию пора бросать")
	}
	if store.fails["7"] == "" {
		t.Error("причина неудачи не записана")
	}
}
