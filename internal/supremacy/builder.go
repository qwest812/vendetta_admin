package supremacy

// Воркер очереди строительства. В отличие от автопилота Мейв, он ходит
// не по расписанию, а к событию: у идущей стройки известно время окончания,
// у ресурсов — запас и прирост, поэтому партия сама говорит, когда в неё
// возвращаться (см. PlanBuilds). Воркер только просыпается раз в минуту
// и смотрит, чей срок настал.
//
// Заход в партию игра засчитывает как вход, поэтому к назначенному сроку
// добавляется разброс: ровный ритм в такие часы — подпись робота.

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"

	"Vendetta_admin/internal/domain"
)

const (
	// buildTick — как часто смотреть, не пора ли в какую-нибудь партию.
	// Это не заход в игру, а один запрос к своей базе.
	buildTick = time.Minute

	// buildJitter — разброс, на который сдвигается назначенный заход.
	buildJitter = 3 * time.Minute

	// buildRetry — через сколько пробовать партию, в которую не пустили.
	// Чаще незачем: чаще всего это молчание сайта или кончившаяся партия.
	buildRetry = 30 * time.Minute
)

// BuildStore — где лежит очередь и куда писать итог захода. В бою это
// репозиторий, в тестах — заглушка.
type BuildStore interface {
	Due(ctx context.Context, now time.Time) ([]domain.GameTask, error)
	Plan(ctx context.Context, gameID string) (map[int][]int, error)
	Started(ctx context.Context, gameID string, provinceID, upgradeID int) error
	MarkRun(ctx context.Context, gameID string, at, next time.Time, result string) error
}

// buildRunner — тот, кто умеет сходить в партию. Интерфейс ради тестов.
type buildRunner interface {
	RunBuildQueue(ctx context.Context, gameID string, queue map[int][]int) (*BuildRun, error)
}

// Builder обходит партии, у которых настал срок.
type Builder struct {
	client buildRunner
	store  BuildStore
	log    *slog.Logger

	// now и jitter подменяются в тестах: заход назначается со случайным
	// сдвигом, а тест должен видеть ровное время.
	now    func() time.Time
	jitter func() time.Duration
}

func NewBuilder(c buildRunner, store BuildStore, log *slog.Logger) *Builder {
	return &Builder{
		client: c, store: store, log: log,
		now:    time.Now,
		jitter: func() time.Duration { return rand.N(buildJitter) },
	}
}

// Run крутится до отмены контекста.
func (b *Builder) Run(ctx context.Context) {
	b.log.Info("очередь строительства запущена", "проверка", buildTick)

	ticker := time.NewTicker(buildTick)
	defer ticker.Stop()

	for {
		if err := b.tick(ctx); err != nil && ctx.Err() == nil {
			b.log.Error("обход очередей строительства", "err", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (b *Builder) tick(ctx context.Context) error {
	games, err := b.store.Due(ctx, b.now())
	if err != nil {
		return err
	}
	for _, g := range games {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.visit(ctx, g)
	}
	return nil
}

// visit — один заход в одну партию: поставить что можно и назначить
// следующий срок. Итог записывается всегда, и при удаче, и при отказе:
// по нему человек и разбирается, почему очередь стоит.
func (b *Builder) visit(ctx context.Context, g domain.GameTask) {
	queue, err := b.store.Plan(ctx, g.GameID)
	if err != nil {
		b.log.Error("очередь строительства из базы", "gameID", g.GameID, "err", err)
		return
	}
	if len(queue) == 0 {
		return
	}

	now := b.now()
	run, err := b.client.RunBuildQueue(ctx, g.GameID, queue)
	if err != nil {
		b.log.Warn("заход за стройкой", "gameID", g.GameID, "партия", g.Title, "err", err)
		b.mark(ctx, g.GameID, now, now.Add(buildRetry), "Не вышло зайти в партию: "+err.Error())
		return
	}

	// Поставленное уходит из очереди: следующим станет то, что за ним.
	for _, order := range run.Started {
		if err := b.store.Started(ctx, g.GameID, order.ProvinceID, order.UpgradeID); err != nil {
			b.log.Error("запись поставленного здания", "gameID", g.GameID, "err", err)
		}
	}

	next := run.Next
	if next.IsZero() {
		// Ждать нечего: очередь стоит вся. Заглянем через несколько часов —
		// провинции могут вернуться, а ресурсы начать копиться.
		next = now.Add(buildLatest)
	}
	b.mark(ctx, g.GameID, now, next.Add(b.jitter()), buildSummary(run))
}

func (b *Builder) mark(ctx context.Context, gameID string, at, next time.Time, result string) {
	if err := b.store.MarkRun(ctx, gameID, at, next, result); err != nil {
		b.log.Error("запись итога захода за стройкой", "gameID", gameID, "err", err)
	}
}

// buildSummary — итог захода словами. Его читают на странице партии,
// поэтому он и при удаче не пустой: «ничего не поставили» — тоже ответ,
// и к нему нужна причина.
func buildSummary(run *BuildRun) string {
	var parts []string
	if len(run.Started) > 0 {
		var started []string
		for _, o := range run.Started {
			started = append(started, o.Province+" — "+o.Upgrade)
		}
		parts = append(parts, "поставили: "+strings.Join(started, ", "))
	}
	parts = append(parts, run.Failed...)
	parts = append(parts, run.Notes...)
	if len(parts) == 0 {
		return "Очередь пуста или везде идёт стройка."
	}
	// Первая буква заглавная: итог читают строкой на странице партии.
	// По рунам, а не по байтам: буквы здесь русские.
	summary := []rune(strings.Join(parts, "; "))
	summary[0] = unicode.ToUpper(summary[0])
	return string(summary)
}
