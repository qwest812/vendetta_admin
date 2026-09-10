package supremacy

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
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

// Свои игры подписываются тем же вектором, что снят с живого клиента,
// поэтому порядок параметров проверяем прямо по коду.
func TestMyGamesParamsMatchSignature(t *testing.T) {
	params := append(myGamesParams("101408369"),
		param{"authTstamp", "1786039670"},
		param{"authUserID", "101408369"},
		param{"source", trackingSource},
	)

	var b strings.Builder
	for i, p := range params {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.key)
		b.WriteByte('=')
		b.WriteString(p.value)
	}

	const want = "userID=101408369&loadUserLoginData=1&authTstamp=1786039670&authUserID=101408369&source=browser-desktop"
	if got := b.String(); got != want {
		t.Errorf("строка параметров:\n получили %s\n ожидалось %s", got, want)
	}
}

// Ответ снят с живого аккаунта: в отличие от лобби result здесь массив,
// а не объект с games, и на этом легко споткнуться.
func TestParseMyGames(t *testing.T) {
	const raw = `[{"@c":"hup.model.games.Game","properties":{` +
		`"gameID":"10867066","startofgame2":"1785920128","nrofplayers":"31",` +
		`"openSlots":"0","dayofgame":"6","language":"ru",` +
		`"title":"[Speed] - The Great War","state":"running",` +
		`"playerID":"32","joinTime":"1785920263","isSystemGame":true}}]`

	games, err := parseMyGames([]byte(raw))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("игр = %d, ожидалась 1", len(games))
	}

	g := games[0]
	for _, tt := range []struct{ name, got, want string }{
		{"gameID", g.GameID, "10867066"},
		{"title", g.Title, "[Speed] - The Great War"},
		{"state", g.State, "running"},
		{"день", g.DayOfGame, "6"},
		{"playerID", g.PlayerID, "32"},
		{"вход", g.JoinTime, "1785920263"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, ожидалось %q", tt.name, tt.got, tt.want)
		}
	}
}

// Те же поля игра шлёт и числами — так приходит список своих игр с живого
// аккаунта. Раньше на этом падал разбор всего списка, и игр не было ни одной.
func TestParseMyGamesNumbers(t *testing.T) {
	const raw = `[{"@c":"hup.model.games.Game","properties":{` +
		`"gameID":10867066,"startofgame2":1785920128,"nrofplayers":31,` +
		`"openSlots":0,"dayofgame":6,"language":"ru",` +
		`"title":"[Speed] - The Great War","state":"running","timeScale":0.25,` +
		`"playerID":32,"joinTime":1785920263,"isSystemGame":true}}]`

	games, err := parseMyGames([]byte(raw))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(games) != 1 {
		t.Fatalf("игр = %d, ожидалась 1", len(games))
	}

	g := games[0]
	for _, tt := range []struct{ name, got, want string }{
		{"gameID", g.GameID, "10867066"},
		{"title", g.Title, "[Speed] - The Great War"},
		{"state", g.State, "running"},
		{"день", g.DayOfGame, "6"},
		{"игроков", g.NrOfPlayers, "31"},
		{"playerID", g.PlayerID, "32"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, ожидалось %q", tt.name, tt.got, tt.want)
		}
	}
	if got := g.Started().Unix(); got != 1785920128 {
		t.Errorf("начало партии = %d, ожидалось 1785920128", got)
	}
	if got := g.Speed(); got != 4 {
		t.Errorf("скорость = %v, ожидалась 4", got)
	}
}

// Состав партии сайт отдаёт медленно — партию на 500 игроков секунд
// двадцать, — поэтому ответ живёт в кэше. Проверяем без сети: транспорт
// клиента всегда отказывает, значит всё, что вернулось, пришло из кэша.
func TestGameCache(t *testing.T) {
	c := newOfflineClient()
	c.games = map[string]*gameEntry{"10875491": cachedGame(time.Now())}

	game, roster, err := c.Game(context.Background(), "10875491")
	if err != nil {
		t.Fatalf("свежий состав должен браться из кэша: %v", err)
	}
	if game.Title != "The Great War - 500 players" || len(roster) != 1 {
		t.Fatalf("из кэша пришло %+v, состав %+v", game, roster)
	}

	// Партию отдаём копией: страница, поправившая её у себя, не должна
	// испортить ответ следующей.
	game.Title = "чужая правка"
	again, _, err := c.Game(context.Background(), "10875491")
	if err != nil {
		t.Fatalf("повторный вопрос: %v", err)
	}
	if again.Title != "The Great War - 500 players" {
		t.Errorf("кэш испорчен снаружи: %+v", again)
	}
}

// Протухший ответ спрашивается заново — а сети нет, значит и ошибка.
// Молча отдать вчерашний состав было бы хуже: кланы на карте разъедутся
// с настоящими, и понять этого будет нельзя.
func TestGameCacheExpires(t *testing.T) {
	c := newOfflineClient()
	c.games = map[string]*gameEntry{"10875491": cachedGame(time.Now().Add(-rosterTTL - time.Minute))}

	if _, _, err := c.Game(context.Background(), "10875491"); err == nil {
		t.Error("протухший состав должен спрашиваться у сайта заново")
	}
}

// Большую партию держим в кэше дольше маленькой: ответ по ней стоит минуты
// ожидания, а устаревает не быстрее — за полдня в партии успевает поменяться
// разве что чей-то альянс.
func TestBigGameCacheLivesLonger(t *testing.T) {
	c := newOfflineClient()
	old := time.Now().Add(-rosterTTL - time.Minute)
	c.games = map[string]*gameEntry{
		"10875491": bigCachedGame(old),
		"10900334": cachedGame(old),
	}

	if _, _, err := c.Game(context.Background(), "10875491"); err != nil {
		t.Errorf("состав большой партии должен жить дольше получаса: %v", err)
	}
	if _, _, err := c.Game(context.Background(), "10900334"); err == nil {
		t.Error("состав обычной партии через полчаса должен спрашиваться заново")
	}

	// Шесть часов — предел и для большой.
	c.games = map[string]*gameEntry{"10875491": bigCachedGame(time.Now().Add(-bigRosterTTL - time.Minute))}
	if _, _, err := c.Game(context.Background(), "10875491"); err == nil {
		t.Error("через шесть часов состав должен спрашиваться заново")
	}
}

// Размер партии переживает свой же ответ: сам состав протухает, а число
// игроков остаётся — по нему решают, сколько ждать сайт в следующий раз.
func TestGameSizeOutlivesCache(t *testing.T) {
	c := newOfflineClient()
	if _, ok := c.GameSize("10875491"); ok {
		t.Fatal("про партию ещё не спрашивали, размера быть не должно")
	}

	c.noteSize("10875491", 390)
	c.games = map[string]*gameEntry{}

	n, ok := c.GameSize("10875491")
	if !ok || n != 390 {
		t.Errorf("размер = %d (известен=%v), ждали 390", n, ok)
	}
	if n < BigRoster {
		t.Errorf("партия на %d игроков должна считаться большой", n)
	}
}

// Пока одну партию спрашивают, второй спрашивающий ждёт — но не дольше,
// чем нужен его собственной странице.
func TestGameCacheWaitsUnderContext(t *testing.T) {
	c := newOfflineClient()
	entry := cachedGame(time.Now())
	entry.busy <- struct{}{} // партию уже кто-то спрашивает
	c.games = map[string]*gameEntry{"10875491": entry}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Game(ctx, "10875491"); !errors.Is(err, context.Canceled) {
		t.Errorf("ожидание = %v, ожидалась отмена", err)
	}
}

// Протухшие ячейки выбрасываются, но не те, что прямо сейчас заполняют:
// у них ещё нет времени ответа, и по нему они выглядят древними.
func TestGameEntrySweep(t *testing.T) {
	c := newOfflineClient()
	c.games = map[string]*gameEntry{
		"свежая":     cachedGame(time.Now()),
		"протухшая":  cachedGame(time.Now().Add(-rosterTTL - time.Minute)),
		"в процессе": {busy: make(chan struct{}, 1)},
	}

	c.gameEntry("новая")

	for _, id := range []string{"свежая", "в процессе", "новая"} {
		if _, ok := c.games[id]; !ok {
			t.Errorf("ячейка %q пропала", id)
		}
	}
	if _, ok := c.games["протухшая"]; ok {
		t.Error("протухшая ячейка осталась в кэше")
	}
}

func cachedGame(at time.Time) *gameEntry {
	return &gameEntry{
		busy:   make(chan struct{}, 1),
		game:   &Game{GameID: "10875491", Title: "The Great War - 500 players"},
		logins: []GameLogin{{Login: "Vakyla", SiteUserID: "2953349"}},
		at:     at,
	}
}

// bigCachedGame — та же ячейка, но с составом большой партии: важен здесь
// только его размер, поэтому логины одинаковые.
func bigCachedGame(at time.Time) *gameEntry {
	e := cachedGame(at)
	e.logins = make([]GameLogin, BigRoster)
	return e
}

// newOfflineClient — клиент, которому запрещено ходить в сеть: всё, что
// он отдал, он взял из кэша.
func newOfflineClient() *Client {
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: offlineTransport{}}
	return c
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("сети нет")
}

// Лобби отдаёт result объектом, и его формат сюда попасть не должен:
// молча вернуть пустой список — худший исход, список игр выглядел бы
// правдоподобно пустым.
func TestParseMyGamesRejectsLobbyShape(t *testing.T) {
	if _, err := parseMyGames([]byte(`{"numGames":1,"games":[]}`)); err == nil {
		t.Error("ответ лобби должен считаться ошибкой разбора")
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

// Ссылка ведёт в игру под нужным аккаунтом; до логина uid ещё неизвестен,
// и тогда параметра быть не должно — с пустым uid игра открывает не то.
func TestPlayURL(t *testing.T) {
	if got, want := PlayURL("101408369"), "https://www.supremacy1914.com/game.php?bust=1&uid=101408369"; got != want {
		t.Errorf("PlayURL = %s, ожидалось %s", got, want)
	}
	if got, want := PlayURL(""), "https://www.supremacy1914.com/game.php?bust=1"; got != want {
		t.Errorf("PlayURL без аккаунта = %s, ожидалось %s", got, want)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordLogger пишет всё, вплоть до Debug, в буфер — чтобы проверять,
// что именно попало в лог.
func recordLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// Сводка нужна на старте сразу, а дальше — не чаще раза в heartbeatEvery:
// при опросе раз в минуту иначе набегает 60 бесполезных строк в час.
func TestWatcherHeartbeatRate(t *testing.T) {
	log, buf := recordLogger()
	w := NewWatcher(nil, nil, time.Minute, log, nil, nil)

	start := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	w.heartbeat(start)
	if n := strings.Count(buf.String(), "воркер лобби жив"); n != 1 {
		t.Fatalf("на старте сводок %d, ожидалась 1", n)
	}

	w.heartbeat(start.Add(heartbeatEvery - time.Minute))
	if n := strings.Count(buf.String(), "воркер лобби жив"); n != 1 {
		t.Errorf("до истечения периода сводок %d, ожидалась 1", n)
	}

	w.heartbeat(start.Add(heartbeatEvery))
	if n := strings.Count(buf.String(), "воркер лобби жив"); n != 2 {
		t.Errorf("после истечения периода сводок %d, ожидалось 2", n)
	}
}

// Сводка отчитывается за период, а не за всё время: иначе счётчики только
// растут и по ним не понять, что происходит сейчас.
func TestWatcherHeartbeatResetsStats(t *testing.T) {
	ctx := context.Background()
	g := Game{GameID: "111", Title: "The Great War"}
	log, buf := recordLogger()
	w := NewWatcher(&stubLister{pages: [][]Game{{g}, {g}}}, nil, time.Minute, log, nil, nil)

	start := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	if err := w.poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	w.heartbeat(start)
	if !strings.Contains(buf.String(), "новых=1") {
		t.Fatalf("в первой сводке нет находки:\n%s", buf)
	}

	buf.Reset()
	if err := w.poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	w.heartbeat(start.Add(heartbeatEvery))
	if !strings.Contains(buf.String(), "новых=0") {
		t.Errorf("во второй сводке счётчик находок не обнулился:\n%s", buf)
	}
	if !strings.Contains(buf.String(), "опросов=1") {
		t.Errorf("во второй сводке счётчик опросов не обнулился:\n%s", buf)
	}
}

// Игра лежит минутами, и все эти минуты ошибка одна и та же. Печатать её
// каждый тик — значит утопить в ней всё остальное.
func TestWatcherThinsOutRepeatedErrors(t *testing.T) {
	log, buf := recordLogger()
	w := NewWatcher(nil, nil, time.Minute, log, nil, nil)

	down := errors.New("getGames: http 502")
	for range errorRepeatEvery + 1 {
		w.noteFailure(down)
	}
	if n := strings.Count(buf.String(), "level=ERROR"); n != 2 {
		t.Errorf("на %d одинаковых отказов пришлось %d записей, ожидалось 2", errorRepeatEvery+1, n)
	}

	// Другая ошибка — другая причина, о ней надо сказать сразу.
	buf.Reset()
	w.noteFailure(errors.New("getGames: resultCode=-19 forbidden"))
	if n := strings.Count(buf.String(), "level=ERROR"); n != 1 {
		t.Errorf("о новой ошибке записей %d, ожидалась 1", n)
	}

	// А по логу, где видно начало сбоя, но не конец, непонятно, живо ли всё.
	buf.Reset()
	w.noteSuccess()
	if !strings.Contains(buf.String(), "восстановился") {
		t.Errorf("о возврате лобби в строй не сообщили:\n%s", buf)
	}
	w.noteSuccess()
	if n := strings.Count(buf.String(), "восстановился"); n != 1 {
		t.Errorf("о восстановлении сообщили %d раз, ожидался 1", n)
	}
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

// Обучающие партии висят в лобби постоянно: о них не сообщаем ни при пустом
// фильтре, ни когда фильтр по названию совпал с их картой.
func TestWatcherSkipsTutorialGames(t *testing.T) {
	const tutorial = "[Tutorial] - The Great War"

	all := NewWatcher(nil, nil, time.Minute, quietLog(), nil, nil)
	if all.matches(tutorial) {
		t.Error("без фильтра обучающая партия всё равно не нужна")
	}
	if !all.matches("[Speed] - The Great War") {
		t.Error("обычная партия должна проходить")
	}

	filtered := NewWatcher(nil, []string{"great war"}, time.Minute, quietLog(), nil, nil)
	if filtered.matches(tutorial) {
		t.Error("фильтр по названию не должен вытаскивать обучающую партию")
	}
	if !filtered.matches("The Great War") {
		t.Error("настоящая партия под тем же фильтром должна проходить")
	}
}

// Альянс сайт отдаёт вложенным в properties, а игроку без альянса кладёт
// в это поле null. Форма взята из живого ответа getUserDetailsFirefly.
func TestParseAlliance(t *testing.T) {
	const withAlliance = `{"@c": "hup.model.users.User", "id": 101408369,
	 "alliance": {"@c": "hup.model.alliances.Alliance",
	  "properties": {"uid": "843930", "name": "VEN.DETTA", "tag": "-V.D-",
	                 "leaderID": "75396888"}}}`

	a, err := parseAlliance([]byte(withAlliance))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if a == nil || a.ID != "843930" || a.Name != "VEN.DETTA" || a.Tag != "-V.D-" {
		t.Errorf("альянс = %+v", a)
	}

	// Ни альянса, ни ошибки: игрок сам по себе.
	for _, body := range []string{`{"id": 1, "alliance": null}`, `{"id": 1}`} {
		a, err := parseAlliance([]byte(body))
		if err != nil || a != nil {
			t.Errorf("%s: альянс = %+v, err = %v", body, a, err)
		}
	}
}

// Состав клана приходит списком рядом с самим кланом. Форма взята
// из живого ответа getAlliance с members=1.
func TestParseRoster(t *testing.T) {
	const body = `{"@c": "hup.model.alliances.Alliance",
	 "properties": {"uid": "843930", "name": "VEN.DETTA", "tag": "-V.D-", "numMembers": 20},
	 "members": [
	   {"@c": "hup.model.users.User", "id": 75396888, "username": "V.D. Noxarion"},
	   {"@c": "hup.model.users.User", "id": 101408369, "username": "Dau7er"},
	   {"@c": "hup.model.users.User", "id": 0, "username": "служебный"}
	 ]}`

	alliance, members, err := parseRoster([]byte(body), "843930")
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if alliance.Name != "VEN.DETTA" || alliance.Tag != "-V.D-" {
		t.Errorf("клан = %+v", alliance)
	}
	// Служебный нулевой номер в состав не попадает: искать его в партии
	// бессмысленно.
	if len(members) != 2 || members[1].SiteUserID != "101408369" || members[1].Name != "Dau7er" {
		t.Errorf("состав = %+v", members)
	}
}

// Ответ без клана — это отказ, а не пустой состав: молча принять его
// значило бы записать всем участникам «клана нет».
func TestParseRosterWithoutAlliance(t *testing.T) {
	if _, _, err := parseRoster([]byte(`{"members": []}`), "843930"); err == nil {
		t.Error("ответ без клана принят за пустой состав")
	}
}

// Состав партии сайт отдаёт вместе с самой партией: ник, номер на сайте,
// клан с названием и уровень. Форма взята из живого ответа getGame.
func TestParseGameLogins(t *testing.T) {
	const body = `{"@c": "hup.model.games.Game",
	 "properties": {"gameID": "10892960", "title": "[Speed] - The Great War",
	                "nrofplayers": "31", "dayofgame": "15"},
	 "logins": [
	   {"login": "Vakyla", "siteUserID": "2953349", "allianceID": "11781",
	    "allianceName": "F L O W", "teamID": "0", "playerLevel": 17},
	   {"login": "MigoV", "siteUserID": "11075095", "allianceID": "0",
	    "teamID": "5", "playerLevel": 13}
	 ]}`

	var res struct {
		Properties Game        `json:"properties"`
		Logins     []GameLogin `json:"logins"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if res.Properties.Title != "[Speed] - The Great War" {
		t.Errorf("партия = %+v", res.Properties)
	}
	if len(res.Logins) != 2 {
		t.Fatalf("состав = %+v", res.Logins)
	}
	if got := res.Logins[0]; !got.InClan() || got.AllianceName != "F L O W" || got.Level != 17 {
		t.Errorf("игрок с кланом = %+v", got)
	}
	// «0» у сайта означает «ни в каком клане», и за клан это считать нельзя.
	if res.Logins[1].InClan() {
		t.Errorf("игрок без клана = %+v", res.Logins[1])
	}
}

// Удалённый игрок остаётся в составе партии, но siteUserID и allianceID
// приходят у него нулём-числом вместо строки. В партии на 500 человек такой
// один ронял разбор всего состава — из-за него не читалась вся партия.
func TestParseGameLoginsDeletedUser(t *testing.T) {
	const body = `{"logins": [
	   {"login": "Fanuel Mota", "siteUserID": "1175522", "allianceID": "453796",
	    "allianceName": "ASSEMBLEIA GERAL DO BRASIL", "teamID": "0", "playerLevel": 18},
	   {"login": "Deleted User", "siteUserID": 0, "allianceID": 0,
	    "faction": "0", "teamID": "0", "achievementTitleItemID": 0}
	 ]}`

	var res struct {
		Logins []GameLogin `json:"logins"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(res.Logins) != 2 {
		t.Fatalf("состав = %+v", res.Logins)
	}
	if got := res.Logins[0]; !got.InClan() || got.Level != 18 {
		t.Errorf("живой игрок = %+v", got)
	}
	gone := res.Logins[1]
	if gone.SiteUserID != "0" || gone.AllianceID != "0" {
		t.Errorf("удалённый игрок = %+v", gone)
	}
	// Ноль клана у удалённого — такое же «ни в каком», как и строковый «0».
	if gone.InClan() {
		t.Errorf("удалённый игрок в клане: %+v", gone)
	}
}

// Время игра шлёт секундами в строке, и мусор в этом поле — обычное дело:
// у партии из лобби нет времени входа, у не начавшейся — времени старта.
func TestGameTimes(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"1785920128", time.Unix(1785920128, 0)},
		{"0", time.Time{}},
		{"", time.Time{}},
		{"null", time.Time{}},
	}
	for _, tt := range tests {
		if got := (Game{StartOfGame: tt.in}).Started(); !got.Equal(tt.want) {
			t.Errorf("Started(%q) = %v, ожидалось %v", tt.in, got, tt.want)
		}
	}
}

// Скорость игра называет обратной величиной, и в двух местах по-разному:
// список лобби шлёт строку, свойства партии — число.
func TestGameSpeed(t *testing.T) {
	tests := []struct {
		raw  string
		want float64
	}{
		{`{"timeScale":"0.25"}`, 4},
		{`{"timeScale":0.25}`, 4},
		{`{"timeScale":"1"}`, 1},
		{`{"timeScale":0.10000000149011612}`, 10},
		// Поля не было вовсе: считаем партию обычной, а не бесконечно быстрой.
		{`{}`, 1},
	}
	for _, tt := range tests {
		var g Game
		if err := json.Unmarshal([]byte(tt.raw), &g); err != nil {
			t.Fatalf("разбор %s: %v", tt.raw, err)
		}
		if got := g.Speed(); got != tt.want {
			t.Errorf("Speed(%s) = %v, ожидалось x%v", tt.raw, got, tt.want)
		}
	}
}

// countingTransport — сеть, которая всегда отказывает и считает попытки.
type countingTransport struct{ tries int }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.tries++
	return nil, errors.New("сети нет")
}

// Сайт игры на частые входы отвечает отказом всем подряд, а каждый вызов
// воркера превращался в новую попытку войти — и отказ только затягивался.
// После неудачи держим паузу: зовущий получает ту же ошибку, но сайт при
// этом не трогаем вовсе.
func TestLoginBackoffStopsRetrying(t *testing.T) {
	tr := &countingTransport{}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}

	if _, err := c.ensureSession(context.Background()); err == nil {
		t.Fatal("без сети вход должен не удаться")
	}
	first := tr.tries
	if first == 0 {
		t.Fatal("первая попытка должна дойти до сети")
	}

	for i := 0; i < 20; i++ {
		if _, err := c.ensureSession(context.Background()); err == nil {
			t.Fatal("во время паузы вход не должен удаваться")
		}
	}
	if tr.tries != first {
		t.Errorf("за паузу сходили в сеть ещё %d раз", tr.tries-first)
	}

	// Пауза кончилась — пробуем снова, и она становится длиннее.
	c.mu.Lock()
	c.loginNextAt = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if _, err := c.ensureSession(context.Background()); err == nil {
		t.Fatal("без сети вход должен не удаться")
	}
	if tr.tries == first {
		t.Error("после паузы попытка должна дойти до сети")
	}
	if c.loginFails != 2 {
		t.Errorf("неудач подряд = %d, ждали 2", c.loginFails)
	}
	if want := loginBackoff[1]; time.Until(c.loginNextAt) <= loginBackoff[0] {
		t.Errorf("вторая пауза должна быть длиннее первой (ждали около %v)", want)
	}
}

// memorySessions — хранилище подписи в памяти, для тестов.
type memorySessions struct {
	saved  SavedSession
	has    bool
	loads  int
	writes int
}

func (m *memorySessions) LoadSession(context.Context) (SavedSession, bool, error) {
	m.loads++
	return m.saved, m.has, nil
}

func (m *memorySessions) SaveSession(_ context.Context, s SavedSession) error {
	m.saved, m.has = s, true
	m.writes++
	return nil
}

// Подпись прошлого запуска избавляет от входа: перезапуск приложения не
// повод входить в игру заново, а каждый лишний вход приближает отказ.
func TestSessionRestoredFromStore(t *testing.T) {
	tr := &countingTransport{}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}
	store := &memorySessions{has: true, saved: SavedSession{
		UserID: "101408369", AuthHash: "d51a4c06", AuthTstamp: "1788", SavedAt: time.Now(),
	}}
	c.UseSessionStore(store)

	sess, err := c.ensureSession(context.Background())
	if err != nil {
		t.Fatalf("подпись из хранилища: %v", err)
	}
	if sess.userID != "101408369" || sess.authHash != "d51a4c06" {
		t.Errorf("взяли не ту подпись: %+v", sess)
	}
	if tr.tries != 0 {
		t.Errorf("при живой подписи в сеть ходить незачем, сходили %d раз", tr.tries)
	}

	// Не подошла — узнаём об этом от игры, и второй раз её не достаём:
	// мёртвая подпись мертва до конца процесса.
	c.invalidate(sess)
	if _, err := c.ensureSession(context.Background()); err == nil {
		t.Fatal("без сети вход должен не удаться")
	}
	if store.loads != 1 {
		t.Errorf("хранилище прочитано %d раз, должно ровно один", store.loads)
	}
}

// Слишком старую подпись не пробуем: сколько она живёт, знает только сервер
// игры, и попытка кончилась бы тем же входом, но с лишним вызовом.
func TestStaleSessionIgnored(t *testing.T) {
	tr := &countingTransport{}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}
	c.UseSessionStore(&memorySessions{has: true, saved: SavedSession{
		UserID: "101408369", AuthHash: "d51a4c06",
		SavedAt: time.Now().Add(-sessionReuse - time.Hour),
	}})

	if _, err := c.ensureSession(context.Background()); err == nil {
		t.Fatal("протухшую подпись брать нельзя, а сети нет — должна быть ошибка")
	}
	if tr.tries == 0 {
		t.Error("вместо старой подписи должен быть вход")
	}
}

// loginTransport — сеть, в которой вход всегда удаётся. Считает, сколько раз
// на самом деле логинились, и держит каждый вход медленным: без задержки
// гонку за подписью не поймать.
type loginTransport struct {
	mu    sync.Mutex
	tries int
	delay time.Duration
}

func (t *loginTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Страница входа: на ней клиент только греет куки.
	if r.Method == http.MethodGet {
		return t.page(r, "<html>вход</html>"), nil
	}

	t.mu.Lock()
	t.tries++
	t.mu.Unlock()
	time.Sleep(t.delay)

	// Удачный вход уводит на game.php, и подпись лежит прямо в нём.
	done := r.Clone(r.Context())
	done.URL = &url.URL{Scheme: "https", Host: "www.supremacy1914.com", Path: "/game.php"}
	return t.page(done, `<script>var s = "uberAuthHash=`+strings.Repeat("a", 40)+
		`&uberAuthTstamp=1788984426&userID=101408369";</script>`), nil
}

func (t *loginTransport) page(r *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    r,
	}
}

// Подпись общая: входит первый, а получают её все сразу. Иначе пятеро
// воркеров, столкнувшихся на протухшей подписи, устроили бы пять входов
// подряд — то самое, за что сайт игры и отказывает.
func TestOneLoginServesEveryone(t *testing.T) {
	tr := &loginTransport{delay: 50 * time.Millisecond}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}

	const callers = 5
	var wg sync.WaitGroup
	got := make([]*session, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess, err := c.ensureSession(context.Background())
			if err != nil {
				t.Errorf("зовущий %d: %v", i, err)
				return
			}
			got[i] = sess
		}(i)
	}
	wg.Wait()

	if tr.tries != 1 {
		t.Errorf("входов было %d, должен быть один на всех", tr.tries)
	}
	for i, sess := range got {
		if sess == nil || sess != got[0] {
			t.Errorf("зовущий %d получил не ту подпись: %v", i, sess)
		}
	}
}

// Ждущий волен уйти по своему сроку: у страницы партии он свой, и стоять
// за чужим входом дольше него незачем. Сам вход при этом идёт дальше —
// он нужен не одной ей, — и подпись достаётся всем следующим.
func TestWaiterLeavesButLoginGoesOn(t *testing.T) {
	tr := &loginTransport{delay: 300 * time.Millisecond}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}

	// Нетерпеливый: его срок кончится посреди входа.
	quick, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.ensureSession(quick); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ждущий должен уйти по своему сроку, а получил %v", err)
	}
	if waited := time.Since(start); waited > 200*time.Millisecond {
		t.Errorf("ждал %v — значит, стоял за чужим входом", waited)
	}

	// Терпеливый приходит следом и получает ту же подпись: своей попытки
	// он не заводит.
	sess, err := c.ensureSession(context.Background())
	if err != nil {
		t.Fatalf("подпись: %v", err)
	}
	if sess == nil || sess.userID != "101408369" {
		t.Errorf("подпись не та: %+v", sess)
	}
	if tr.tries != 1 {
		t.Errorf("входов было %d, должен быть один: уход ждущего вход не отменяет", tr.tries)
	}
}

// Уход ждущего не считается неудачей входа: иначе закрытая вкладка ставила
// бы всем паузу на полминуты.
func TestWaiterLeavingIsNotAFailure(t *testing.T) {
	tr := &loginTransport{delay: 200 * time.Millisecond}
	c := NewClient("user", "pass", "ru", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.http = &http.Client{Transport: tr}

	quick, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.ensureSession(quick); err == nil {
		t.Fatal("ждущий должен уйти по своему сроку")
	}
	if _, err := c.ensureSession(context.Background()); err != nil {
		t.Fatalf("вход должен был дойти до конца: %v", err)
	}

	c.mu.Lock()
	fails, next := c.loginFails, c.loginNextAt
	c.mu.Unlock()
	if fails != 0 || !next.IsZero() {
		t.Errorf("неудач %d, пауза до %v — а неудачи не было", fails, next)
	}
}

// Список своих партий спрашивает не только раздел «Игры»: страница любой
// партии смотрит в него, чтобы понять, играем мы в ней или смотрим со
// стороны. Это запрос на каждый просмотр, поэтому список держим минуту —
// иначе сотня открытий стоит сотни запросов к сайту.
func TestMyGamesCached(t *testing.T) {
	c := newOfflineClient()
	c.mine.games = []Game{{GameID: "10900334", Title: "[Speed] - The Great War"}}
	c.mine.at = time.Now()

	games, err := c.MyGames(context.Background())
	if err != nil {
		t.Fatalf("свежий список должен браться из кэша: %v", err)
	}
	if len(games) != 1 || games[0].GameID != "10900334" {
		t.Fatalf("из кэша пришло %+v", games)
	}

	// Список отдаётся копией: страница держит в руках ссылки на его строки.
	games[0].Title = "чужая правка"
	again, err := c.MyGames(context.Background())
	if err != nil {
		t.Fatalf("повторный вопрос: %v", err)
	}
	if again[0].Title != "[Speed] - The Great War" {
		t.Errorf("кэш испорчен снаружи: %+v", again)
	}

	// Через минуту спрашиваем заново — а сети нет, значит и ошибка.
	c.mine.at = time.Now().Add(-myGamesTTL - time.Second)
	if _, err := c.MyGames(context.Background()); err == nil {
		t.Error("протухший список должен спрашиваться у сайта заново")
	}
}
