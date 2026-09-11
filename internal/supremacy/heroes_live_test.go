//go:build live

package supremacy

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/supremacy/heroes"
)

// Живая сборка справочника героев. В обычный прогон не входит: ходит
// в сеть — в витрину магазина и в открытые файлы клиента.
//
//	set -a; . ./.env; set +a
//	go test -tags live -run TestHeroesFetchLive -v ./internal/supremacy/
//	HEROES_WRITE=1 …   — ещё и переписать вшитый снимок, это make heroes
//
// Подпись берётся из базы, а не добывается входом: сайт наказывает
// за частые входы, а справочник того не стоит.
type dbSess struct{ s *repo.Settings }

func (d dbSess) LoadSession(ctx context.Context) (SavedSession, bool, error) {
	v, ok, err := d.s.LoadSession(ctx)
	if err != nil || !ok {
		return SavedSession{}, false, err
	}
	return SavedSession{UserID: v.UserID, AuthHash: v.AuthHash, AuthTstamp: v.AuthTstamp, SavedAt: v.SavedAt}, true, nil
}
func (d dbSess) SaveSession(context.Context, SavedSession) error { return nil }

func TestHeroesFetchLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	c := NewClient(os.Getenv("S1914_USER"), os.Getenv("S1914_PASSWORD"), os.Getenv("S1914_LANG"),
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.UseSessionStore(dbSess{repo.NewSettings(pool)})

	offers, err := c.HeroOffers(ctx)
	if err != nil {
		t.Fatalf("витрина героев: %v", err)
	}
	t.Logf("офферов: %d", len(offers))

	prev, err := heroes.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	snap, images, err := heroes.Fetch(ctx, nil, offers, prev)
	if err != nil {
		t.Fatalf("сбор справочника: %v", err)
	}
	t.Logf("клиент %s, героев %d, картинок %d", snap.Client, len(snap.Heroes), len(images))

	if os.Getenv("HEROES_WRITE") == "1" {
		raw, err := json.MarshalIndent(snap, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("heroes/heroes.json", append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, data := range images {
			if err := os.WriteFile("heroes/images/"+name, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("снимок записан в heroes/")
	}
	for _, h := range snap.Heroes {
		eff := 0
		for _, l := range h.Levels {
			eff += len(l.Effects)
		}
		t.Logf("  %6d %-16s %-16s уровней=%-3d эффектов=%-3d умений=%d картинка=%v",
			h.UnitTypeID, h.ShortEn, h.ShortRu, len(h.Levels), eff, len(h.Skills), len(images[h.Image]) > 0)
	}
}
