package supremacy

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

// Векторы сняты с живого клиента игры: параметры и итоговый hash из URL
// реальных запросов, uberAuthHash — со страницы game.php той же сессии.
func TestSignature(t *testing.T) {
	const uberAuthHash = "972aaafed1ae5bcf62802ac6545383544163ed21"

	tests := []struct {
		name   string
		action string
		params string
		want   string
	}{
		{
			name:   "мои игры",
			action: "getGames",
			params: "userID=101408369&loadUserLoginData=1&authTstamp=1786039670&authUserID=101408369&source=browser-desktop",
			want:   "8f548bc624a0da2fa2393a62656ae85c5df78ba1",
		},
		{
			name:   "архив",
			action: "getGames",
			params: "userID=101408369&mygamesMode=archived&loadUserLoginData=1&authTstamp=1786039670&authUserID=101408369&source=browser-desktop",
			want:   "d864300a38d3e5b9194fd67ca21ccfd4788afefc",
		},
		{
			name:   "лобби",
			action: "getGames",
			params: "numEntriesPerPage=2&page=1&lang=en&password=0&allianceGame=0&openSlots=1&isFilterSearch=false&global=1&loadUserLoginData=1&authTstamp=1786039670&authUserID=101408369&source=browser-desktop",
			want:   "733d0e6f604b72315281ab0f259465da6bedc76b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum := sha1.Sum([]byte(apiKey + tt.action + tt.params + uberAuthHash))
			if got := hex.EncodeToString(sum[:]); got != tt.want {
				t.Errorf("подпись = %s, ожидалось %s", got, tt.want)
			}
		})
	}
}

// Параметры лобби должны собираться ровно в том порядке и виде, что и в
// подписи из TestSignature, иначе игра отвергнет запрос.
func TestOpenGamesParamsMatchSignature(t *testing.T) {
	params := []param{
		{"numEntriesPerPage", "2"},
		{"page", "1"},
		{"lang", "en"},
		{"password", "0"},
		{"allianceGame", "0"},
		{"openSlots", "1"},
		{"isFilterSearch", "false"},
		{"global", "1"},
		{"loadUserLoginData", "1"},
		{"authTstamp", "1786039670"},
		{"authUserID", "101408369"},
		{"source", trackingSource},
	}

	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(p.value)
	}

	const want = "numEntriesPerPage=2&page=1&lang=en&password=0&allianceGame=0&openSlots=1&isFilterSearch=false&global=1&loadUserLoginData=1&authTstamp=1786039670&authUserID=101408369&source=browser-desktop"
	if got := b.String(); got != want {
		t.Errorf("строка параметров:\n получили %s\n ожидалось %s", got, want)
	}
}

func TestEncodeURIComponent(t *testing.T) {
	tests := []struct{ in, want string }{
		{"browser-desktop", "browser-desktop"},
		{"false", "false"},
		{"The Great War", "The%20Great%20War"},
		{"a&b=c", "a%26b%3Dc"},
		{"don't!", "don't!"},
		{"ру", "%D1%80%D1%83"},
	}
	for _, tt := range tests {
		if got := encodeURIComponent(tt.in); got != tt.want {
			t.Errorf("encodeURIComponent(%q) = %q, ожидалось %q", tt.in, got, tt.want)
		}
	}
}

// Перезаходить стоит только на протухшей сессии: на прочих отказах игры
// релогин не поможет, а логиниться будем каждый тик.
func TestNeedsRelogin(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want bool
	}{
		{"сессия протухла", &APIError{Code: codeSessionExpired}, true},
		{"аутентификация не прошла", &APIError{Code: codeAuthenticationFailed}, true},
		{"ответ не JSON", &APIError{Malformed: true}, true},
		{"http 403", &APIError{Status: 403}, true},
		{"действие запрещено", &APIError{Code: -19}, false},
		{"не хватает параметра", &APIError{Code: -20}, false},
		{"неизвестная ошибка", &APIError{Code: -1}, false},
		{"http 500", &APIError{Status: 500}, false},
	}
	for _, tt := range tests {
		if got := tt.err.needsRelogin(); got != tt.want {
			t.Errorf("%s: needsRelogin = %v, ожидалось %v", tt.name, got, tt.want)
		}
	}
}

// stubLister отдаёт заранее заданные ответы, по одному на вызов.
type stubLister struct {
	pages [][]Game
	calls int
}

func (s *stubLister) OpenGames(context.Context) ([]Game, error) {
	page := s.pages[s.calls]
	s.calls++
	return page, nil
}

// collectNotifier запоминает, о чём сообщили.
type collectNotifier struct {
	got  []string
	fail bool
}

func (c *collectNotifier) Notify(_ context.Context, g Game) error {
	if c.fail {
		return errors.New("уведомление не ушло")
	}
	c.got = append(c.got, g.GameID)
	return nil
}

func TestWatcherReportsEachGameOnce(t *testing.T) {
	great := Game{GameID: "111", Title: "The Great War"}
	speed := Game{GameID: "222", Title: "[Speed] - The Great War"}
	other := Game{GameID: "333", Title: "Shattered America"}

	lister := &stubLister{pages: [][]Game{
		{great, other},        // сообщаем про 111
		{great, speed, other}, // 111 уже видели, новый — 222
		{other},               // подходящих нет, кеш не трогаем
		{great, speed},        // обе уже в кеше — молчим
	}}
	notifier := &collectNotifier{}
	w := NewWatcher(lister, []string{"great war"}, time.Minute, discardLogger(), notifier, nil)

	for range lister.pages {
		if err := w.poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	want := []string{"111", "222"}
	if !slices.Equal(notifier.got, want) {
		t.Errorf("сообщили о %v, ожидалось %v", notifier.got, want)
	}
}

// Игра, ушедшая из лобби и вернувшаяся, второй раз сообщаться не должна.
func TestWatcherRemembersGoneGames(t *testing.T) {
	g := Game{GameID: "111", Title: "The Great War"}
	lister := &stubLister{pages: [][]Game{{g}, {}, {g}}}
	notifier := &collectNotifier{}
	w := NewWatcher(lister, []string{"great war"}, time.Minute, discardLogger(), notifier, nil)

	for range lister.pages {
		if err := w.poll(context.Background()); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	if want := []string{"111"}; !slices.Equal(notifier.got, want) {
		t.Errorf("сообщили о %v, ожидалось %v", notifier.got, want)
	}
}

// Неудачное уведомление не должно занимать место в кеше — иначе о находке
// не узнают вообще никогда.
func TestWatcherRetriesAfterNotifyError(t *testing.T) {
	ctx := context.Background()
	g := Game{GameID: "111", Title: "The Great War"}
	lister := &stubLister{pages: [][]Game{{g}, {g}}}
	notifier := &collectNotifier{fail: true}
	store := NewMemorySeenStore()
	w := NewWatcher(lister, []string{"great war"}, time.Minute, discardLogger(), notifier, store)

	if err := w.poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	seen, err := store.Seen(ctx, []string{"111"})
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("после ошибки игра попала в кеш, а не должна была")
	}

	notifier.fail = false
	if err := w.poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if want := []string{"111"}; !slices.Equal(notifier.got, want) {
		t.Errorf("сообщили о %v, ожидалось %v", notifier.got, want)
	}
}

// Кеш в хранилище, а не в воркере, поэтому пересозданный воркер (это и есть
// перезапуск приложения) не должен сообщать о том же заново.
func TestWatcherCacheSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	g := Game{GameID: "111", Title: "The Great War"}
	store := NewMemorySeenStore()

	first := &collectNotifier{}
	w := NewWatcher(&stubLister{pages: [][]Game{{g}}}, nil, time.Minute, discardLogger(), first, store)
	if err := w.poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if want := []string{"111"}; !slices.Equal(first.got, want) {
		t.Fatalf("первый воркер сообщил о %v, ожидалось %v", first.got, want)
	}

	second := &collectNotifier{}
	restarted := NewWatcher(&stubLister{pages: [][]Game{{g}}}, nil, time.Minute, discardLogger(), second, store)
	if err := restarted.poll(ctx); err != nil {
		t.Fatalf("poll после перезапуска: %v", err)
	}
	if len(second.got) != 0 {
		t.Errorf("после перезапуска сообщили о %v, а должны были промолчать", second.got)
	}
}

func TestMemorySeenStoreForget(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	store := NewMemorySeenStore()

	if err := store.Mark(ctx, "свежая", "A", now.Add(-time.Hour)); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := store.Mark(ctx, "протухшая", "B", now.Add(-seenRetention-time.Minute)); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	n, err := store.Forget(ctx, now.Add(-seenRetention))
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if n != 1 {
		t.Errorf("убрано %d записей, ожидалась 1", n)
	}

	seen, err := store.Seen(ctx, []string{"свежая", "протухшая"})
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if !seen["свежая"] {
		t.Error("свежая запись пропала из кеша")
	}
	if seen["протухшая"] {
		t.Error("протухшая запись осталась в кеше")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWatcherMatches(t *testing.T) {
	w := NewWatcher(nil, []string{" The Great War ", "", "mesopotamia"}, 0, discardLogger(), nil, nil)

	yes := []string{"The Great War", "[Speed] - The Great War", "the great war - 500 players", "[Dominion] - Mesopotamia"}
	for _, title := range yes {
		if !w.matches(title) {
			t.Errorf("%q должно совпасть", title)
		}
	}
	no := []string{"Shattered America", "World in flames", "Game of Janiproo12"}
	for _, title := range no {
		if w.matches(title) {
			t.Errorf("%q не должно совпасть", title)
		}
	}
}

// Без заданных названий воркер отдаёт всё лобби целиком.
func TestWatcherWithoutTitlesMatchesEverything(t *testing.T) {
	for _, titles := range [][]string{nil, {}, {"", "   "}} {
		w := NewWatcher(nil, titles, 0, discardLogger(), nil, nil)
		for _, title := range []string{"The Great War", "Game of LilDark", ""} {
			if !w.matches(title) {
				t.Errorf("titles=%v: %q должно совпасть", titles, title)
			}
		}
	}
}
