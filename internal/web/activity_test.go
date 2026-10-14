package web

import (
	"testing"
	"time"

	"Vendetta_admin/internal/supremacy"
)

func TestActivityClass(t *testing.T) {
	now := time.Now()
	cases := []struct {
		p    supremacy.Player
		want string
	}{
		{supremacy.Player{LastLogin: now.Add(-2 * time.Hour)}, "act-today"},
		{supremacy.Player{LastLogin: now.Add(-48 * time.Hour)}, "act-recent"},
		{supremacy.Player{LastLogin: now.Add(-100 * time.Hour)}, "act-old"},
		{supremacy.Player{Inactive: true}, "act-gone"},
		{supremacy.Player{IsAI: true, LastLogin: now}, ""},
		{supremacy.Player{}, ""},
	}
	for _, c := range cases {
		if got := activityClass(c.p, now); got != c.want {
			t.Errorf("%+v → %q, want %q", c.p, got, c.want)
		}
	}
}
