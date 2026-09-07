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

type fakeTopStore struct {
	at      *time.Time
	saved   []domain.TopAlliance
	savedAt time.Time
	writes  int
}

func (f *fakeTopStore) CapturedAt(context.Context) (*time.Time, error) { return f.at, nil }

func (f *fakeTopStore) Replace(_ context.Context, list []domain.TopAlliance, at time.Time) error {
	f.saved = list
	f.savedAt = at
	f.writes++
	return nil
}

type fakeRanking struct {
	entries []RankedAlliance
	err     error
	calls   int
}

func (f *fakeRanking) AllianceRanking(_ context.Context, page, numEntries int) ([]RankedAlliance, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if page != 0 || numEntries != topSize {
		return nil, errors.New("топ спрашивают одной первой страницей")
	}
	return f.entries, nil
}

func topWatcher(store TopStore, src topSource, now time.Time) *TopWatcher {
	w := NewTopWatcher(src, store, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.now = func() time.Time { return now }
	return w
}

// Пустая база — повод сходить за рейтингом сразу: до первого обхода режим
// топа на карте не показывает ничего.
func TestTopWatcherFirstRun(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeTopStore{}
	src := &fakeRanking{entries: []RankedAlliance{
		{Alliance: Alliance{ID: "212184", Name: "Operation Blitzkrieg", Tag: "OP BG"}, Rank: 1, Elo: 1581},
		{Alliance: Alliance{ID: "127956", Name: "Pride of LIONS", Tag: "*SNG*"}, Rank: 2, Elo: 1561},
	}}

	if err := topWatcher(store, src, now).tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 2 || store.saved[0].ID != "212184" || store.saved[0].Rank != 1 {
		t.Fatalf("снимок = %+v", store.saved)
	}
	if store.saved[0].Elo != 1581 || store.saved[1].Tag != "*SNG*" {
		t.Errorf("очки и тег не доехали: %+v", store.saved)
	}
	if !store.savedAt.Equal(now) {
		t.Errorf("время снимка = %v", store.savedAt)
	}
}

// Свежий снимок трогать незачем: рейтинг такой глубины меняется месяцами,
// а лишний обход — это поход на чужой сайт без всякой пользы.
func TestTopWatcherKeepsFresh(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-2 * 24 * time.Hour)
	store := &fakeTopStore{at: &fresh}
	src := &fakeRanking{}

	if err := topWatcher(store, src, now).tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.calls != 0 || store.writes != 0 {
		t.Errorf("сходили за рейтингом зря: запросов %d, записей %d", src.calls, store.writes)
	}
}

// Протухший снимок обновляется.
func TestTopWatcherRefreshesStale(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	store := &fakeTopStore{at: &old}
	src := &fakeRanking{entries: []RankedAlliance{
		{Alliance: Alliance{ID: "6309", Name: "Legion Imperial"}, Rank: 3},
	}}

	if err := topWatcher(store, src, now).tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || len(store.saved) != 1 || store.saved[0].ID != "6309" {
		t.Fatalf("снимок не обновился: записей %d, %+v", store.writes, store.saved)
	}
}

// Пустой ответ рейтинга — это сбой на той стороне, а не «в игре не осталось
// кланов». Старый снимок в таком случае полезнее пустой таблицы.
func TestTopWatcherKeepsSnapshotOnEmptyAnswer(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	store := &fakeTopStore{at: &old}
	src := &fakeRanking{}

	if err := topWatcher(store, src, now).tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 {
		t.Errorf("пустой рейтинг стёр снимок")
	}
}

// Разбор ответа рейтинга: клан лежит в properties, место и очки — в stats.
func TestParseRanking(t *testing.T) {
	raw := []byte(`{"entries":[
		{"properties":{"uid":"212184","name":"Operation Blitzkrieg","tag":"OP BG"},
		 "stats":{"elo":1581,"globalRank":1}},
		{"properties":{"uid":"","name":"Битый"},"stats":{"elo":1,"globalRank":2}}
	]}`)

	got, err := parseRanking(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Запись без номера клана пропускается: спросить по ней состав нечем.
	if len(got) != 1 {
		t.Fatalf("разобрано %d записей: %+v", len(got), got)
	}
	if got[0].ID != "212184" || got[0].Rank != 1 || got[0].Elo != 1581 || got[0].Tag != "OP BG" {
		t.Errorf("запись = %+v", got[0])
	}
}
