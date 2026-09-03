package supremacy

// Кланы игроков спрашивает этот воркер, а не страница карты. Причина
// простая: сайт игры отвечает про одного игрока за раз, а в партии их до
// сорока — открытие карты превращалось бы в сорок запросов и в ожидание
// у человека перед экраном. Поэтому страница только записывает встреченных
// игроков в базу и рисует то, что там уже есть, а воркер разбирает очередь
// маленькими порциями в своём темпе.

import (
	"context"
	"log/slog"
	"time"

	"Vendetta_admin/internal/domain"
)

const (
	// allianceTTL — сколько ответ игры считается годным. Клан меняют редко,
	// а очередь тем длиннее, чем короче срок.
	allianceTTL = 24 * time.Hour

	// allianceBatch — сколько игроков воркер берёт за тик. Это же число
	// запросов к сайту: тик должен заканчиваться быстро и не выглядеть
	// со стороны игры как обход базы пользователей.
	allianceBatch = 50

	// rosterBatch — сколько кланов за тик забираем целиком. Их меньше,
	// чем игроков, и каждый стоит одного запроса, зато закрывает сразу
	// весь свой состав.
	rosterBatch = 5
)

// AllianceStore — очередь и кеш кланов. В бою это репозиторий, в тестах —
// заглушка.
type AllianceStore interface {
	Stale(ctx context.Context, olderThan time.Time, limit int) ([]string, error)
	Save(ctx context.Context, list []domain.Alliance, at time.Time) error
	// Кланы: очередь на состав и запись забранного.
	EnqueueAlliances(ctx context.Context, allianceIDs []string) error
	StaleAlliances(ctx context.Context, olderThan time.Time, limit int) ([]string, error)
	SaveRoster(ctx context.Context, alliance domain.Alliance, members []domain.Alliance, at time.Time) error
}

// allianceSource — тот, кто умеет спросить сайт. Интерфейс ради тестов:
// в бою сюда приходит *Client.
type allianceSource interface {
	UserAlliances(ctx context.Context, siteUserIDs []string) (map[string]*Alliance, error)
	AllianceRoster(ctx context.Context, allianceID string) (*Alliance, []AllianceMember, error)
}

// AllianceWatcher раз в interval разбирает очередь: сначала тех, о ком
// ещё не спрашивали, потом самых давних.
type AllianceWatcher struct {
	client   allianceSource
	store    AllianceStore
	log      *slog.Logger
	interval time.Duration

	// ttl и порции вынесены в поля ради тестов; в бою это константы выше.
	ttl     time.Duration
	batch   int
	rosters int

	now func() time.Time
}

func NewAllianceWatcher(c allianceSource, store AllianceStore,
	interval time.Duration, log *slog.Logger) *AllianceWatcher {

	return &AllianceWatcher{
		client: c, store: store, log: log, interval: interval,
		ttl: allianceTTL, batch: allianceBatch, rosters: rosterBatch, now: time.Now,
	}
}

// Run крутится до отмены контекста. Первый разбор делает сразу: после
// перезапуска в очереди обычно уже кто-то есть.
func (w *AllianceWatcher) Run(ctx context.Context) {
	w.log.Info("обновляем кланы Supremacy", "interval", w.interval, "срок", w.ttl)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if err := w.tick(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("обновление кланов", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick разбирает одну порцию. Сначала составы кланов: один запрос закрывает
// сразу весь клан, и после него спрашивать про его игроков по отдельности
// уже не придётся. Потом — оставшиеся игроки, по одному: только так узнаётся
// клан того, кто нам ещё не встречался.
//
// Пустые очереди — обычное дело: значит, всё свежее, и до следующего тика
// делать нечего.
func (w *AllianceWatcher) tick(ctx context.Context) error {
	rosterErr := w.rosterTick(ctx)
	usersErr := w.usersTick(ctx)
	if rosterErr != nil {
		return rosterErr
	}
	return usersErr
}

// rosterTick забирает составы кланов целиком.
func (w *AllianceWatcher) rosterTick(ctx context.Context) error {
	ids, err := w.store.StaleAlliances(ctx, w.now().Add(-w.ttl), w.rosters)
	if err != nil || len(ids) == 0 {
		return err
	}

	var firstErr error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		alliance, members, err := w.client.AllianceRoster(ctx, id)
		if err != nil {
			// Один недоступный клан не отменяет остальных: он останется
			// в очереди и попробуется на следующем тике.
			w.log.Warn("состав клана", "allianceID", id, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		rows := make([]domain.Alliance, 0, len(members))
		for _, m := range members {
			rows = append(rows, domain.Alliance{SiteUserID: m.SiteUserID})
		}
		row := domain.Alliance{ID: alliance.ID, Name: alliance.Name, Tag: alliance.Tag}
		if err := w.store.SaveRoster(ctx, row, rows, w.now()); err != nil {
			return err
		}
		w.log.Info("состав клана забран",
			"клан", alliance.Name, "allianceID", id, "игроков", len(rows))
	}
	return firstErr
}

// usersTick спрашивает про тех, кого не закрыл ни один состав.
func (w *AllianceWatcher) usersTick(ctx context.Context) error {
	ids, err := w.store.Stale(ctx, w.now().Add(-w.ttl), w.batch)
	if err != nil || len(ids) == 0 {
		return err
	}

	// Ошибка на части номеров не отменяет остальных: что узнали — то
	// и запишем, а недоспрошенные останутся в очереди на следующий тик.
	fetched, askErr := w.client.UserAlliances(ctx, ids)
	if len(fetched) == 0 {
		return askErr
	}

	list := make([]domain.Alliance, 0, len(fetched))
	seen := map[string]bool{}
	var clans []string
	for id, a := range fetched {
		row := domain.Alliance{SiteUserID: id}
		if a != nil {
			row.ID, row.Name, row.Tag = a.ID, a.Name, a.Tag
			// Новый клан сразу ставим в очередь на состав: следующий тик
			// закроет им всех его игроков разом.
			if !seen[a.ID] {
				seen[a.ID] = true
				clans = append(clans, a.ID)
			}
		}
		list = append(list, row)
	}
	if err := w.store.Save(ctx, list, w.now()); err != nil {
		return err
	}
	if err := w.store.EnqueueAlliances(ctx, clans); err != nil {
		return err
	}
	w.log.Info("кланы игроков обновлены",
		"спрошено", len(ids), "получено", len(fetched), "новых кланов", len(clans))
	return askErr
}
