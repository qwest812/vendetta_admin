package supremacy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
)

type fakeHuntStore struct {
	targets []domain.HuntTarget
	seen    map[string]bool
	hits    []domain.HuntHit
}

func (f *fakeHuntStore) Targets(context.Context) ([]domain.HuntTarget, error) { return f.targets, nil }

func (f *fakeHuntStore) Seen(_ context.Context, target int64, gameID string) (bool, error) {
	return f.seen[gameID], nil
}

func (f *fakeHuntStore) Record(_ context.Context, h domain.HuntHit) error {
	f.hits = append(f.hits, h)
	f.seen[h.GameID] = true
	return nil
}

type fakeLobby struct {
	games   []Game
	logins  map[string][]GameLogin
	state   *GameState
	lobbyed int
}

func (f *fakeLobby) OpenGames(context.Context) ([]Game, error) {
	f.lobbyed++
	return f.games, nil
}

func (f *fakeLobby) Game(_ context.Context, id string) (*Game, []GameLogin, error) {
	for _, g := range f.games {
		if g.GameID == id {
			g := g
			return &g, f.logins[id], nil
		}
	}
	return nil, nil, errors.New("нет партии")
}

func (f *fakeLobby) ObserveGame(context.Context, string) (*GameState, error) {
	if f.state == nil {
		return nil, errors.New("не пустили")
	}
	return f.state, nil
}

type fakeHuntNotifier struct {
	sent []domain.HuntHit
	err  error
}

func (f *fakeHuntNotifier) NotifyHunt(_ context.Context, h domain.HuntHit, _ Game) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, h)
	return nil
}

func huntFixture() (*fakeLobby, *fakeHuntStore) {
	lobby := &fakeLobby{
		games: []Game{{GameID: "1", Title: "Мировая война"}, {GameID: "2", Title: "Европа"}},
		logins: map[string][]GameLogin{
			"1": {{Login: "Someone", SiteUserID: "5"}},
			"2": {{Login: "Dau7er", SiteUserID: "42"}},
		},
		state: &GameState{Players: map[int]Player{3: {ID: 3, SiteUserID: "42", Nation: "Швеция"}}},
	}
	store := &fakeHuntStore{
		targets: []domain.HuntTarget{{ID: 7, SiteUserID: "42", Nickname: "старый ник"}},
		seen:    map[string]bool{},
	}
	return lobby, store
}

// Искать некого — в лобби не ходим вовсе.
func TestHunterIdleWithoutTargets(t *testing.T) {
	lobby, store := huntFixture()
	store.targets = nil
	h := NewHunter(lobby, store, &fakeHuntNotifier{}, 10*time.Minute, quietLog())
	if err := h.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lobby.lobbyed != 0 {
		t.Fatalf("без искомых ходили в лобби %d раз", lobby.lobbyed)
	}
}

// Нашёлся — одно сообщение с ником из лобби и страной, и отметка. Во второй
// проход о той же партии уже не пишем.
func TestHunterFindsOnce(t *testing.T) {
	lobby, store := huntFixture()
	notify := &fakeHuntNotifier{}
	h := NewHunter(lobby, store, notify, 10*time.Minute, quietLog())
	h.pause = 0

	for range 2 {
		if err := h.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(notify.sent) != 1 {
		t.Fatalf("сообщений %d, ожидалось одно", len(notify.sent))
	}
	got := notify.sent[0]
	if got.GameID != "2" || got.Nickname != "Dau7er" || got.Nation != "Швеция" || got.TargetID != 7 {
		t.Errorf("находка: %+v", got)
	}
	if len(store.hits) != 1 {
		t.Errorf("записано находок %d", len(store.hits))
	}
}

// Сообщение не ушло — отметки нет: скажем на следующем проходе.
func TestHunterRetriesWhenNotSent(t *testing.T) {
	lobby, store := huntFixture()
	h := NewHunter(lobby, store, &fakeHuntNotifier{err: errors.New("телеграм лежит")}, 10*time.Minute, quietLog())
	h.pause = 0
	if err := h.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.hits) != 0 {
		t.Error("неотправленная находка записана")
	}
}

// Страна не узналась — сообщение всё равно уходит, без страны.
func TestHunterWithoutNation(t *testing.T) {
	lobby, store := huntFixture()
	lobby.state = nil
	notify := &fakeHuntNotifier{}
	h := NewHunter(lobby, store, notify, 10*time.Minute, quietLog())
	h.pause = 0
	if err := h.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notify.sent) != 1 || notify.sent[0].Nation != "" {
		t.Fatalf("ожидалось сообщение без страны: %+v", notify.sent)
	}
}

func TestHuntText(t *testing.T) {
	text := huntText(
		domain.HuntHit{Nickname: "Dau<7>er", Nation: "Швеция"},
		Game{GameID: "10910172", Title: "Мировая война", OpenSlots: "3", NrOfPlayers: "31", DayOfGame: "1"},
		"https://example/play")
	for _, want := range []string{
		"🔎 <b>Dau&lt;7&gt;er</b> в партии <b>Мировая война</b>",
		"за Швеция · свободно 3 · игроков 31 · день 1",
		`<a href="https://example/play">открыть</a> · id 10910172`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в сообщении нет %q:\n%s", want, text)
		}
	}
}
