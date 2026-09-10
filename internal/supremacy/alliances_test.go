package supremacy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

type fakeAllianceStore struct {
	queue      []string
	saved      []domain.Alliance
	at         time.Time
	clanQueue  []string
	enqueued   []string
	rosters    map[string][]string
	rosterName map[string]string
}

func (f *fakeAllianceStore) Stale(_ context.Context, _ time.Time, limit int) ([]string, error) {
	if len(f.queue) > limit {
		return f.queue[:limit], nil
	}
	return f.queue, nil
}

func (f *fakeAllianceStore) Save(_ context.Context, list []domain.Alliance, at time.Time) error {
	f.saved = append(f.saved, list...)
	f.at = at
	return nil
}

func (f *fakeAllianceStore) EnqueueAlliances(_ context.Context, ids []string) error {
	f.enqueued = append(f.enqueued, ids...)
	return nil
}

func (f *fakeAllianceStore) StaleAlliances(_ context.Context, _ time.Time, limit int) ([]string, error) {
	if len(f.clanQueue) > limit {
		return f.clanQueue[:limit], nil
	}
	return f.clanQueue, nil
}

func (f *fakeAllianceStore) SaveRoster(_ context.Context, alliance domain.Alliance,
	members []domain.Alliance, _ time.Time) error {

	if f.rosters == nil {
		f.rosters = map[string][]string{}
		f.rosterName = map[string]string{}
	}
	f.rosterName[alliance.ID] = alliance.Name
	for _, m := range members {
		f.rosters[alliance.ID] = append(f.rosters[alliance.ID], m.SiteUserID)
	}
	return nil
}

type fakeAllianceSource struct {
	found   map[string]*Alliance
	err     error
	asked   []string
	roster  map[string][]AllianceMember
	rosters []string
	// rosterErr — чем отвечать на любой запрос состава. Пустая — отвечаем
	// как обычно, по roster.
	rosterErr error
}

func (f *fakeAllianceSource) UserAlliances(_ context.Context, ids []string) (map[string]*Alliance, error) {
	f.asked = append(f.asked, ids...)
	return f.found, f.err
}

func (f *fakeAllianceSource) AllianceRoster(_ context.Context, id string) (*Alliance, []AllianceMember, error) {
	f.rosters = append(f.rosters, id)
	if f.rosterErr != nil {
		return nil, nil, f.rosterErr
	}
	members, ok := f.roster[id]
	if !ok {
		return nil, nil, errors.New("такого клана нет")
	}
	return &Alliance{ID: id, Name: "VEN.DETTA", Tag: "-V.D-"}, members, nil
}

func testWatcher(c allianceSource, store AllianceStore) *AllianceWatcher {
	w := NewAllianceWatcher(c, store, time.Minute,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w
}

// Обычный тик: воркер берёт очередь, спрашивает сайт и кладёт ответы
// в базу. Игрок без клана записывается наравне с остальными — иначе его
// спрашивали бы заново каждый тик.
func TestAllianceWatcherTick(t *testing.T) {
	store := &fakeAllianceStore{queue: []string{"1", "2"}}
	src := &fakeAllianceSource{found: map[string]*Alliance{
		"1": {ID: "843930", Name: "VEN.DETTA", Tag: "-V.D-"},
		"2": nil,
	}}

	if err := testWatcher(src, store).tick(context.Background()); err != nil {
		t.Fatalf("тик: %v", err)
	}

	if len(store.saved) != 2 {
		t.Fatalf("записано %d ответов: %+v", len(store.saved), store.saved)
	}
	saved := map[string]domain.Alliance{}
	for _, a := range store.saved {
		saved[a.SiteUserID] = a
	}
	if got := saved["1"]; got.ID != "843930" || got.Name != "VEN.DETTA" || got.Tag != "-V.D-" {
		t.Errorf("клан игрока 1 = %+v", got)
	}
	if got := saved["2"]; got.ID != "" || got.Name != "" {
		t.Errorf("одиночка записан как %+v, ожидался пустой клан", got)
	}
}

// Пустая очередь — обычное дело: значит, всё свежее. В сеть в этом случае
// ходить не за чем.
func TestAllianceWatcherIdle(t *testing.T) {
	store := &fakeAllianceStore{}
	src := &fakeAllianceSource{}

	if err := testWatcher(src, store).tick(context.Background()); err != nil {
		t.Fatalf("тик: %v", err)
	}
	if len(src.asked) != 0 {
		t.Errorf("спросили %v при пустой очереди", src.asked)
	}
}

// Порция ограничена: за тик воркер делает столько запросов, сколько взял
// номеров, и брать всю очередь целиком ему нельзя.
func TestAllianceWatcherBatch(t *testing.T) {
	store := &fakeAllianceStore{}
	for i := range allianceBatch + 10 {
		store.queue = append(store.queue, fmt.Sprintf("игрок-%d", i))
	}
	src := &fakeAllianceSource{found: map[string]*Alliance{}}

	if err := testWatcher(src, store).tick(context.Background()); err != nil {
		t.Fatalf("тик: %v", err)
	}
	if len(src.asked) != allianceBatch {
		t.Errorf("спрошено %d игроков, ожидалось %d", len(src.asked), allianceBatch)
	}
}

// Отказ сайта на части номеров не отменяет остальных: что узнали — то
// и записываем, ошибка при этом остаётся видимой.
func TestAllianceWatcherPartialFailure(t *testing.T) {
	store := &fakeAllianceStore{queue: []string{"1", "2"}}
	src := &fakeAllianceSource{
		found: map[string]*Alliance{"1": {ID: "843930", Name: "VEN.DETTA"}},
		err:   errors.New("сайт устал"),
	}

	err := testWatcher(src, store).tick(context.Background())
	if err == nil {
		t.Error("ошибка сайта потерялась")
	}
	if len(store.saved) != 1 || store.saved[0].SiteUserID != "1" {
		t.Errorf("записано %+v, ожидался только успевший ответ", store.saved)
	}
}

// Состав клана забирается одним запросом и закрывает сразу всех своих
// игроков — ради этого он и нужен.
func TestAllianceWatcherRoster(t *testing.T) {
	store := &fakeAllianceStore{clanQueue: []string{"843930"}}
	src := &fakeAllianceSource{roster: map[string][]AllianceMember{
		"843930": {{SiteUserID: "1", Name: "Dau7er"}, {SiteUserID: "2", Name: "Noxarion"}},
	}}

	if err := testWatcher(src, store).tick(context.Background()); err != nil {
		t.Fatalf("тик: %v", err)
	}

	if got := store.rosters["843930"]; len(got) != 2 {
		t.Fatalf("записан состав %v, ожидались оба игрока", got)
	}
	if store.rosterName["843930"] != "VEN.DETTA" {
		t.Errorf("название клана = %q", store.rosterName["843930"])
	}
	// Про самих игроков по отдельности не спрашивали: состав их уже закрыл.
	if len(src.asked) != 0 {
		t.Errorf("лишние запросы по игрокам: %v", src.asked)
	}
}

// Клан, встреченный у игрока, сразу ставится в очередь на состав:
// следующий тик закроет им весь клан одним запросом.
func TestAllianceWatcherEnqueuesClan(t *testing.T) {
	store := &fakeAllianceStore{queue: []string{"1", "2"}}
	src := &fakeAllianceSource{found: map[string]*Alliance{
		"1": {ID: "843930", Name: "VEN.DETTA"},
		"2": {ID: "843930", Name: "VEN.DETTA"},
	}}

	if err := testWatcher(src, store).tick(context.Background()); err != nil {
		t.Fatalf("тик: %v", err)
	}

	// Клан один, хоть игроков и двое: очередь не должна пухнуть повторами.
	if len(store.enqueued) != 1 || store.enqueued[0] != "843930" {
		t.Errorf("в очередь на состав попало %v", store.enqueued)
	}
}

// Недоступный клан не отменяет остальную работу тика: он остаётся
// в очереди, а игроков всё равно спрашиваем.
func TestAllianceWatcherRosterFailure(t *testing.T) {
	store := &fakeAllianceStore{clanQueue: []string{"нет такого"}, queue: []string{"1"}}
	src := &fakeAllianceSource{
		roster: map[string][]AllianceMember{},
		found:  map[string]*Alliance{"1": nil},
	}

	if err := testWatcher(src, store).tick(context.Background()); err == nil {
		t.Error("отказ по составу потерялся")
	}
	if len(src.asked) != 1 {
		t.Errorf("игроков спросили %v, ожидался один", src.asked)
	}
}

// Если вход в игру отложен, круг обрывается на первом же клане: остальные
// упрутся в то же самое, а полсотни одинаковых строк в логе только прячут
// настоящую причину. Очередь при этом цела — вернёмся на следующем тике.
func TestAllianceWatcherStopsWhenLoginPaused(t *testing.T) {
	store := &fakeAllianceStore{clanQueue: []string{"1", "2", "3"}}
	src := &fakeAllianceSource{rosterErr: fmt.Errorf("%w: ещё 5m0s", ErrLoginPaused)}

	err := testWatcher(src, store).rosterTick(context.Background())
	if !errors.Is(err, ErrLoginPaused) {
		t.Errorf("тик вернул %v, ждали отложенный вход", err)
	}
	if len(src.rosters) != 1 {
		t.Errorf("спросили составов: %d, ждали один", len(src.rosters))
	}
}
