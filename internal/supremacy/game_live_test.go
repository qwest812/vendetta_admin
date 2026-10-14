//go:build live

package supremacy

import (
	"context"
	"encoding/json"
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

// Живая проверка газеты: формат сводок о войнах игра не документирует,
// а переводит его на язык аккаунта. Наблюдателем, входом это не считается.
//
//	GAME=10886819 go test -tags live -run TestGameLossesLive -v ./internal/supremacy/
func TestGameLossesLive(t *testing.T) {
	gameID := os.Getenv("GAME")
	if gameID == "" {
		t.Skip("нужен GAME=<gameID>")
	}
	c := NewClient(os.Getenv("S1914_USER"), os.Getenv("S1914_PASSWORD"), os.Getenv("S1914_LANG"),
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	g, err := c.ObserveGame(ctx, gameID)
	if err != nil {
		t.Fatalf("состояние партии: %v", err)
	}
	acc, err := c.gameAccess(ctx, gameID)
	if err != nil {
		t.Fatal(err)
	}
	// Сырой ответ одного дня — чтобы увидеть формат, если разбор промахнётся.
	var raw json.RawMessage
	if err := c.gsCall(ctx, acc, gameID, 2, "ultshared.action.UltUpdateGameStateAction", 0,
		map[string]any{"stateType": 2, "stateID": "", "addStateIDsOnSent": false,
			"option": max(g.Day-1, 1), "actions": nil, "lastCallDuration": 50}, &raw); err != nil {
		t.Fatalf("день газеты: %v", err)
	}
	articles, err := paperArticles(raw)
	t.Logf("статей за день %d: %d (err %v)", max(g.Day-1, 1), len(articles), err)
	for i, a := range articles {
		if i < 5 {
			t.Logf("статья %d: %q", i, a)
		}
	}

	losses, err := c.GameLosses(ctx, gameID, g.Day)
	if err != nil {
		t.Fatalf("потери: %v", err)
	}
	t.Logf("дней %d, не отдано %v, игроков с потерями %d", losses.Days, losses.Missing, len(losses.Players))
	for i, p := range losses.Players {
		if i < 10 {
			t.Logf("%s (#%d): потерял %d, убил %d, противников %d", p.Name, p.ID, p.Lost, p.Killed, len(p.Opponent))
		}
	}
}
