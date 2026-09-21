package supremacy

// Поиск игроков в лобби. Рут отмечает игрока, и воркер раз в несколько
// минут смотрит составы открытых партий: как только искомый окажется
// в одной из них, в телеграм уходит сообщение — кто и в какой партии.
//
// Состава в самом списке лобби нет, поэтому на каждую открытую партию —
// свой запрос к сайту (getGame). Открытых партий немного (десяток-другой),
// и чаще раза в десять минут не ходим: это просьба пользователя, и сайт
// наказывает за частоту. Когда искать некого, в лобби не ходим вовсе.
//
// Страну игра называет только в состоянии партии, поэтому за ней — один
// заход наблюдателем, и только когда игрок нашёлся. Входом в партию это
// не считается, и партия про такой заход не знает.

import (
	"context"
	"log/slog"
	"time"

	"Vendetta_admin/internal/domain"
)

// HuntStore — кого ищем и где уже нашли.
type HuntStore interface {
	Targets(ctx context.Context) ([]domain.HuntTarget, error)
	Seen(ctx context.Context, targetID int64, gameID string) (bool, error)
	Record(ctx context.Context, h domain.HuntHit) error
}

// HuntNotifier — куда сообщать о находке. В бою это телеграм.
type HuntNotifier interface {
	NotifyHunt(ctx context.Context, hit domain.HuntHit, g Game) error
}

// huntClient — что поиску нужно от игры. Интерфейс ради тестов.
type huntClient interface {
	OpenGames(ctx context.Context) ([]Game, error)
	Game(ctx context.Context, gameID string) (*Game, []GameLogin, error)
	ObserveGame(ctx context.Context, gameID string) (*GameState, error)
}

// Hunter — воркер поиска.
type Hunter struct {
	client huntClient
	store  HuntStore
	notify HuntNotifier // nil — только в лог
	log    *slog.Logger

	every time.Duration
	// pause — между запросами состава партий: торопиться некуда.
	pause time.Duration
	now   func() time.Time
}

func NewHunter(c huntClient, store HuntStore, notify HuntNotifier, every time.Duration, log *slog.Logger) *Hunter {
	return &Hunter{client: c, store: store, notify: notify, log: log,
		every: every, pause: time.Second, now: time.Now}
}

// Run крутится до отмены контекста.
func (h *Hunter) Run(ctx context.Context) {
	h.log.Info("поиск игроков в лобби запущен", "проверка", h.every)

	ticker := time.NewTicker(h.every)
	defer ticker.Stop()

	for {
		if err := h.tick(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("поиск игроков в лобби", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Hunter) tick(ctx context.Context) error {
	targets, err := h.store.Targets(ctx)
	if err != nil || len(targets) == 0 {
		return err
	}
	want := make(map[string]domain.HuntTarget, len(targets))
	for _, t := range targets {
		want[t.SiteUserID] = t
	}

	games, err := h.client.OpenGames(ctx)
	if err != nil {
		return err
	}
	for i, g := range games {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i > 0 && h.pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(h.pause):
			}
		}
		// Одна партия не ответила — остальные всё равно смотрим.
		game, logins, err := h.client.Game(ctx, g.GameID)
		if err != nil {
			h.log.Warn("поиск игроков: состав партии", "gameID", g.GameID, "err", err)
			continue
		}
		if game == nil {
			game = &g
		}
		for _, l := range logins {
			if t, ok := want[l.SiteUserID]; ok && l.Known() {
				h.found(ctx, t, l, *game)
			}
		}
	}
	return nil
}

// found сообщает о находке, если о ней ещё не сообщали. Отметка ставится
// только после того, как сообщение ушло: не ушло — скажем на следующем
// проходе.
func (h *Hunter) found(ctx context.Context, t domain.HuntTarget, l GameLogin, g Game) {
	seen, err := h.store.Seen(ctx, t.ID, g.GameID)
	if err != nil {
		h.log.Error("поиск игроков: база", "err", err)
		return
	}
	if seen {
		return
	}

	hit := domain.HuntHit{
		TargetID: t.ID, SiteUserID: t.SiteUserID, Nickname: l.Login,
		GameID: g.GameID, Title: g.Title, Nation: h.nation(ctx, g.GameID, t.SiteUserID),
		FoundAt: h.now(),
	}
	if hit.Nickname == "" {
		hit.Nickname = t.Nickname
	}

	if h.notify != nil {
		if err := h.notify.NotifyHunt(ctx, hit, g); err != nil {
			h.log.Warn("поиск игроков: сообщение не ушло", "gameID", g.GameID, "игрок", hit.Nickname, "err", err)
			return
		}
	}
	if err := h.store.Record(ctx, hit); err != nil {
		h.log.Error("поиск игроков: запись находки", "err", err)
		return
	}
	h.log.Info("игрок найден в лобби", "игрок", hit.Nickname, "gameID", g.GameID,
		"партия", g.Title, "страна", hit.Nation)
}

// nation — за какую страну игрок в партии. Не вышло узнать — пусто:
// сообщение нужнее страны.
func (h *Hunter) nation(ctx context.Context, gameID, siteUserID string) string {
	st, err := h.client.ObserveGame(ctx, gameID)
	if err != nil {
		h.log.Warn("поиск игроков: страна не узналась", "gameID", gameID, "err", err)
		return ""
	}
	for _, p := range st.Players {
		if p.SiteUserID == siteUserID {
			return p.Nation
		}
	}
	return ""
}
