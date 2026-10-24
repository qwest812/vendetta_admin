package web

import (
	"testing"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// Рост считается от самого раннего снимка в окне: уровень и число
// уничтоженных — разницей, кд — разницей отношений, доля побед — в
// процентных пунктах. Снимок того же дня ничего не говорит.
func TestTrend(t *testing.T) {
	today := time.Date(2026, 10, 23, 15, 0, 0, 0, time.Local)
	now := domain.UserStats{Level: 18, Defeated: 3000, Casualties: 1500, Games: 30, SoloWins: 2, CoalitionWins: 13}
	then := repo.Snapshot{Day: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC),
		Level: 16, Defeated: 2000, Casualties: 1250, Games: 20, Wins: 12}

	tr := trend(now, then, today)
	if tr == nil {
		t.Fatal("роста нет, а снимок месячной давности")
	}
	got := []string{tr.Level, tr.KD, tr.WinRate, tr.Defeated}
	want := []string{"+2", "+0.40", "−10", "+1 000"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("поле %d: %q, ожидали %q", i, got[i], want[i])
		}
	}
	if tr.Days != 30 || tr.LevelUp != 1 || tr.WinRateUp != -1 {
		t.Errorf("дни и знаки: %+v", tr)
	}

	// Кд упал — минус типографский, как у остальных чисел.
	down := trend(domain.UserStats{Level: 18, Defeated: 1000, Casualties: 1000}, repo.Snapshot{Day: then.Day, Level: 18, Defeated: 1500, Casualties: 1000}, today)
	if down.KD != "−0.50" || down.KDUp != -1 || down.Level != "0" {
		t.Errorf("падение: %+v", down)
	}

	if trend(now, repo.Snapshot{Day: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)}, today) != nil {
		t.Error("снимок того же дня дал рост")
	}
}
