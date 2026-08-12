package supremacy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Notifier получает игру, название которой совпало с искомым. В бою это
// TelegramNotifier; logNotifier остаётся запасным, когда телеграм не настроен.
type Notifier interface {
	Notify(ctx context.Context, g Game) error
}

// gameLister — то, что умеет отдавать лобби. Интерфейс нужен ради тестов,
// в бою сюда приходит *Client.
type gameLister interface {
	OpenGames(ctx context.Context) ([]Game, error)
}

// seenRetention — сколько помним игру после того, как сообщили о ней.
// Дольше недели держать незачем: висеть в лобби игра столько не может,
// так что запись к этому моменту заведомо мёртвая.
const seenRetention = 7 * 24 * time.Hour

const (
	// heartbeatEvery — как часто подводить итог в Info. Печатать каждый опрос
	// — это строка в минуту ни о чём, а молчать совсем нельзя: по логу тогда
	// не отличить работающий воркер от намертво вставшего.
	heartbeatEvery = time.Hour

	// errorRepeatEvery — через сколько одинаковых подряд отказов повторить
	// жалобу. Игра лежит минутами, и без прореживания лог за это время
	// состоит из одной и той же строки.
	errorRepeatEvery = 10
)

// SeenStore помнит игры, о которых уже сообщили. Реализация на базе переживает
// перезапуск приложения; MemorySeenStore — нет, и годится разве что для тестов.
type SeenStore interface {
	// Seen отбирает из переданных ID те, о которых уже сообщали.
	Seen(ctx context.Context, gameIDs []string) (map[string]bool, error)
	// Mark запоминает игру. Повторная отметка того же ID ничего не меняет.
	Mark(ctx context.Context, gameID, title string, at time.Time) error
	// Forget выметает записи старше указанного момента.
	Forget(ctx context.Context, olderThan time.Time) (int64, error)
}

// Watcher раз в interval смотрит лобби и сообщает о подходящих играх.
type Watcher struct {
	client   gameLister
	log      *slog.Logger
	notifier Notifier
	store    SeenStore
	titles   []string
	interval time.Duration

	// now подменяется в тестах; в бою это time.Now.
	now func() time.Time

	// Всё дальнейшее принадлежит горутине Run и синхронизации не требует.

	// Сводка с прошлого heartbeat.
	stats    pollStats
	lastBeat time.Time

	// Отказы подряд: сколько их набралось и на чём именно, чтобы отличить
	// «всё та же ошибка» от новой.
	failures int
	lastFail string
}

// pollStats копит то, о чём отчитывается heartbeat. Счётчики опросов и ошибок
// накопительные за период, а games/matched — снимок последнего опроса:
// складывать их бессмысленно, одна и та же игра попадала бы в сумму
// каждый тик.
type pollStats struct {
	polls    int
	fails    int
	reported int
	games    int
	matched  int
}

// NewWatcher создаёт воркер. Совпадение по названию — подстрока без учёта
// регистра, так что «great war» поймает и «The Great War», и его спид-версию.
// Пустой titles означает «без фильтра»: сообщаем про все игры лобби.
// Пустой store означает кеш в памяти — он не переживёт перезапуск.
func NewWatcher(c gameLister, titles []string, interval time.Duration, log *slog.Logger, n Notifier, store SeenStore) *Watcher {
	norm := make([]string, 0, len(titles))
	for _, t := range titles {
		if t = strings.TrimSpace(t); t != "" {
			norm = append(norm, strings.ToLower(t))
		}
	}
	if n == nil {
		// Ссылка должна быть той же, что и в телеграме, а аккаунт для неё
		// знает клиент — если, конечно, сюда пришёл он, а не стаб из теста.
		uid := func() string { return "" }
		if u, ok := c.(interface{ UserID() string }); ok {
			uid = u.UserID
		}
		n = logNotifier{log, uid}
	}
	if store == nil {
		store = NewMemorySeenStore()
	}
	return &Watcher{
		client:   c,
		log:      log,
		notifier: n,
		store:    store,
		titles:   norm,
		interval: interval,
		now:      time.Now,
	}
}

// Run крутится до отмены контекста. Первый проход делает сразу, не дожидаясь
// первого тика.
func (w *Watcher) Run(ctx context.Context) {
	titles := any(w.titles)
	if len(w.titles) == 0 {
		titles = "все игры"
	}
	w.log.Info("следим за лобби Supremacy", "titles", titles, "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		err := w.poll(ctx)
		switch {
		case ctx.Err() != nil:
			// Опрос оборвала остановка приложения — жаловаться не на что.
			return
		case err != nil:
			w.noteFailure(err)
		default:
			w.noteSuccess()
		}
		w.heartbeat(w.now())

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// noteFailure печатает отказ, прореживая повторы одного и того же.
func (w *Watcher) noteFailure(err error) {
	w.stats.fails++

	if msg := err.Error(); msg != w.lastFail {
		w.failures = 1
		w.lastFail = msg
		w.log.Error("опрос лобби", "err", err)
		return
	}
	w.failures++
	if w.failures%errorRepeatEvery == 0 {
		w.log.Error("опрос лобби не удаётся", "err", err, "подряд", w.failures)
	}
}

// noteSuccess сообщает, что лобби снова отвечает. Без этой строки в логе
// видно начало сбоя, но не его конец.
func (w *Watcher) noteSuccess() {
	if w.failures == 0 {
		return
	}
	w.log.Info("опрос лобби восстановился", "неудач подряд", w.failures)
	w.failures = 0
	w.lastFail = ""
}

// heartbeat раз в heartbeatEvery подводит итог в Info. Первый вызов печатает
// сразу: подтверждение, что воркер поднялся и лобби отвечает, нужно на старте,
// а не через час.
func (w *Watcher) heartbeat(now time.Time) {
	if !w.lastBeat.IsZero() && now.Sub(w.lastBeat) < heartbeatEvery {
		return
	}
	w.log.Info("воркер лобби жив",
		"опросов", w.stats.polls,
		"неудач", w.stats.fails,
		"новых", w.stats.reported,
		"лобби", w.stats.games,
		"подходящих", w.stats.matched,
	)
	w.lastBeat = now
	w.stats = pollStats{}
}

func (w *Watcher) poll(ctx context.Context) error {
	games, err := w.client.OpenGames(ctx)
	if err != nil {
		return err
	}

	now := w.now()

	// Чистка кеша некритична: не вышла — просто попробуем в следующий раз.
	if n, err := w.store.Forget(ctx, now.Add(-seenRetention)); err != nil {
		w.log.Error("чистка кеша игр", "err", err)
	} else if n > 0 {
		w.log.Debug("из кеша игр убраны старые записи", "count", n)
	}

	matched := make([]Game, 0, len(games))
	ids := make([]string, 0, len(games))
	for _, g := range games {
		if w.matches(g.Title) {
			matched = append(matched, g)
			ids = append(ids, g.GameID)
		}
	}

	seen, err := w.store.Seen(ctx, ids)
	if err != nil {
		return fmt.Errorf("проверка кеша игр: %w", err)
	}

	var reported int
	for _, g := range matched {
		if seen[g.GameID] {
			continue
		}
		if err := w.notifier.Notify(ctx, g); err != nil {
			// В кеш не кладём: попробуем сообщить на следующем тике.
			w.log.Error("уведомление", "gameID", g.GameID, "err", err)
			continue
		}
		if err := w.store.Mark(ctx, g.GameID, g.Title, now); err != nil {
			// Сообщить уже сообщили, так что на следующем тике повторимся.
			w.log.Error("запись в кеш игр", "gameID", g.GameID, "err", err)
			continue
		}
		reported++
	}

	w.stats.polls++
	w.stats.reported += reported
	w.stats.games = len(games)
	w.stats.matched = len(matched)

	w.log.Debug("лобби опрошено",
		"всего", len(games), "совпало", len(matched), "новых", reported)
	return nil
}

func (w *Watcher) matches(title string) bool {
	// Фильтр не задан — берём всё, что висит в лобби.
	if len(w.titles) == 0 {
		return true
	}
	lower := strings.ToLower(title)
	for _, t := range w.titles {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

type logNotifier struct {
	log    *slog.Logger
	userID func() string
}

func (n logNotifier) Notify(_ context.Context, g Game) error {
	n.log.Info("найдена игра",
		"title", g.Title,
		"gameID", g.GameID,
		"свободно", g.OpenSlots,
		"игроков", g.NrOfPlayers,
		"день", g.DayOfGame,
		"url", PlayURL(n.userID()),
	)
	return nil
}
