package supremacy

// Верхушку рейтинга кланов забирает этот воркер. Отдельно от воркера кланов
// потому, что спрашивает другое и совсем в другом темпе: состав клана живёт
// сутки, а первая десятка рейтинга меняется месяцами, и ходить за ней чаще
// незачем.

import (
	"context"
	"log/slog"
	"time"

	"Vendetta_admin/internal/domain"
)

const (
	// topSize — сколько мест рейтинга нас интересует.
	topSize = 10

	// topTTL — сколько снимок топа считается годным. Месяц: рейтинг такой
	// глубины за это время почти не шевелится, а пометка на карте от одного
	// лишнего дня не портится.
	topTTL = 30 * 24 * time.Hour
)

// TopStore — снимок топа в базе. В бою это репозиторий, в тестах заглушка.
type TopStore interface {
	CapturedAt(ctx context.Context) (*time.Time, error)
	Replace(ctx context.Context, list []domain.TopAlliance, at time.Time) error
}

// topSource — тот, кто умеет спросить рейтинг. Интерфейс ради тестов.
type topSource interface {
	AllianceRanking(ctx context.Context, page, numEntries int) ([]RankedAlliance, error)
}

// TopWatcher раз в interval смотрит, не пора ли обновить снимок топа,
// и обновляет, если пора. Сам интервал короче срока годности намеренно:
// иначе после перезапуска или суток простоя снимок обновлялся бы не через
// месяц, а через месяц с хвостом.
type TopWatcher struct {
	client   topSource
	store    TopStore
	log      *slog.Logger
	interval time.Duration

	// ttl и size вынесены в поля ради тестов; в бою это константы выше.
	ttl  time.Duration
	size int

	now func() time.Time
}

func NewTopWatcher(c topSource, store TopStore,
	interval time.Duration, log *slog.Logger) *TopWatcher {

	return &TopWatcher{
		client: c, store: store, log: log, interval: interval,
		ttl: topTTL, size: topSize, now: time.Now,
	}
}

// Run крутится до отмены контекста. Первую проверку делает сразу: если базу
// подняли только что, топа в ней ещё нет вовсе, а карта без него не покажет
// в своём режиме ничего.
func (w *TopWatcher) Run(ctx context.Context) {
	w.log.Info("следим за топом кланов", "interval", w.interval, "срок", w.ttl, "мест", w.size)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if err := w.tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("обновление топа кланов", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick обновляет снимок, если он протух. Свежий снимок — обычный исход:
// значит, до следующего раза делать нечего.
func (w *TopWatcher) tick(ctx context.Context) error {
	at, err := w.store.CapturedAt(ctx)
	if err != nil {
		return err
	}
	now := w.now()
	if at != nil && now.Sub(*at) < w.ttl {
		return nil
	}

	// Топ приходит одной страницей: десять мест меньше любого разумного
	// размера страницы, листать нечего.
	ranked, err := w.client.AllianceRanking(ctx, 0, w.size)
	if err != nil {
		return err
	}
	if len(ranked) == 0 {
		// Пустой рейтинг — это не «топа нет», а «сайт ответил ничем».
		// Старый снимок в таком случае лучше нового пустого места.
		w.log.Warn("рейтинг кланов ответил пустым списком, снимок не трогаем")
		return nil
	}

	list := make([]domain.TopAlliance, 0, len(ranked))
	for _, a := range ranked {
		list = append(list, domain.TopAlliance{
			ID: a.ID, Rank: a.Rank, Elo: a.Elo, Name: a.Name, Tag: a.Tag, CapturedAt: now,
		})
	}
	if err := w.store.Replace(ctx, list, now); err != nil {
		return err
	}
	w.log.Info("топ кланов обновлён", "кланов", len(list), "первый", list[0].Name)
	return nil
}
