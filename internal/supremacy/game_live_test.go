//go:build live

package supremacy

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// Живая проверка захода в партию: форматы игрового сервера мы не выбираем,
// и меняются они без предупреждения. Тест не входит в обычный прогон —
// заход на игровой сервер игра засчитывает как вход в партию.
//
//	set -a; . ./.env; set +a
//	GAME=10886819 go test -tags live -run TestGameStateLive -v ./internal/supremacy/
func TestGameStateLive(t *testing.T) {
	gameID := os.Getenv("GAME")
	if gameID == "" {
		t.Skip("нужен GAME=<gameID>")
	}

	c := NewClient(os.Getenv("S1914_USER"), os.Getenv("S1914_PASSWORD"), os.Getenv("S1914_LANG"),
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	g, err := c.GameState(ctx, gameID)
	if err != nil {
		t.Fatalf("состояние партии: %v", err)
	}

	if g.Me <= 0 {
		t.Fatalf("наш playerID = %d", g.Me)
	}
	if len(g.Provinces) == 0 || len(g.Players) == 0 {
		t.Fatalf("пустое состояние: провинций %d, игроков %d", len(g.Provinces), len(g.Players))
	}
	me, ok := g.Players[g.Me]
	if !ok || me.Nation == "" {
		t.Errorf("в списке игроков нет нас самих или страна пустая: %+v", me)
	}
	t.Logf("день %d, играем за %s (%s), своих провинций %d из %d",
		g.Day, me.Nation, me.Name, len(g.Owned(g.Me)), len(g.Provinces))

	// Номера игроков на сайте печатаем целиком: по ним партия сводится
	// с карточками в базе и с личными списками, и когда сведение ломается,
	// смотреть надо сюда.
	for _, p := range g.Players {
		t.Logf("игрок %3d %-16s %-16s siteUserID=%-12s ии=%v выбыл=%v",
			p.ID, p.Nation, p.Name, p.SiteUserID, p.IsAI, p.Defeated || p.Retired)
	}
}
