package web

import (
	"testing"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// Игра отдаёт время строкой с секундами, а порядок игр — как ей удобно.
// Наверх ставим самую свежую партию: за ней и следят.
func TestGameViews(t *testing.T) {
	views := gameViews([]supremacy.Game{
		{GameID: "1", Title: "старая", State: "running", StartOfGame: "1785920128"},
		{GameID: "2", Title: "свежая", State: "running", StartOfGame: "1785999999"},
		{GameID: "3", Title: "без даты", State: "readytojoin", StartOfGame: "0"},
	})

	got := []string{views[0].ID, views[1].ID, views[2].ID}
	want := []string{"2", "1", "3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порядок игр = %v, ожидался %v", got, want)
		}
	}
	if !views[2].Started.IsZero() {
		t.Error("нулевое время должно оставаться нулевым, а не 1970 годом")
	}
	if views[0].State != "идёт" || views[2].State != "набор" {
		t.Errorf("состояния = %q и %q", views[0].State, views[2].State)
	}
}

// Незнакомое состояние лучше показать как есть, чем спрятать: список игр
// молча потерял бы смысл.
func TestGameStateKeepsUnknown(t *testing.T) {
	if got := gameState("paused"); got != "paused" {
		t.Errorf("gameState(paused) = %q", got)
	}
}

func TestUnixTime(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"1785920128", time.Unix(1785920128, 0)},
		{"0", time.Time{}},
		{"", time.Time{}},
		{"null", time.Time{}},
	}
	for _, tt := range tests {
		if got := unixTime(tt.in); !got.Equal(tt.want) {
			t.Errorf("unixTime(%q) = %v, ожидалось %v", tt.in, got, tt.want)
		}
	}
}
