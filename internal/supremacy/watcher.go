package supremacy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Notifier получает игру, название которой совпало с искомым.
// Пока реализация одна — лог; на её место встанет телеграм.
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

	// О первом удачном опросе отчитываемся в Info: иначе при нулевом улове
	// в логах не видно вообще ничего и непонятно, работает ли воркер.
	// Дальше переходим на Debug, чтобы не сорить строкой каждую минуту.
	polled bool
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
		n = logNotifier{log}
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
		if err := w.poll(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("опрос лобби", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Watcher) poll(ctx context.Context) error {
	games, err := w.client.OpenGames(ctx)
	if err != nil {
		return err
	}

	now := time.Now()

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

	level := slog.LevelDebug
	if !w.polled {
		level = slog.LevelInfo
		w.polled = true
	}
	w.log.Log(ctx, level, "лобби опрошено",
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

type logNotifier struct{ log *slog.Logger }

func (n logNotifier) Notify(_ context.Context, g Game) error {
	n.log.Info("найдена игра",
		"title", g.Title,
		"gameID", g.GameID,
		"свободно", g.OpenSlots,
		"игроков", g.NrOfPlayers,
		"день", g.DayOfGame,
		"url", g.URL(),
	)
	return nil
}
