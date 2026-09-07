package web

import (
	"testing"
	"time"
)

// Таймеру нужны целые секунды, и округление вверх тут не придирка: «0»
// при неистёкшем сроке обещал бы новую копию, которой ещё нет.
func TestWaitSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{0, 0},
		{-time.Second, 0},
		{time.Millisecond, 1},
		{time.Second, 1},
		{1500 * time.Millisecond, 2},
		{time.Minute, 60},
	}
	for _, c := range cases {
		if got := waitSeconds(c.in); got != c.want {
			t.Errorf("waitSeconds(%v) = %d, ждали %d", c.in, got, c.want)
		}
	}
}

// Долгое ожидание показывается временем, а не числом секунд.
func TestWaitClock(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "0:00"},
		{0, "0:00"},
		{7, "0:07"},
		{42, "0:42"},
		{60, "1:00"},
		{2951, "49:11"},
		{5400, "90:00"},
	}
	for _, c := range cases {
		if got := waitClock(c.in); got != c.want {
			t.Errorf("waitClock(%d) = %q, ждали %q", c.in, got, c.want)
		}
	}
}

// Сбор данных должен выглядеть одинаково, сколько бы страница ни собиралась
// на самом деле: мгновенный ответ из общего кэша выдал бы, что партию уже
// смотрели. Дольше положенного никого не держим — время уже отсижено.
func TestLoaderMs(t *testing.T) {
	// Страница собралась мгновенно: ждать остаётся почти всё время сбора.
	for i := 0; i < 20; i++ {
		got := loaderMs(0)
		if got < int(loaderFloor/time.Millisecond) ||
			got > int((loaderFloor+loaderJitter)/time.Millisecond) {
			t.Fatalf("loaderMs(0) = %d, ждали от %d до %d", got,
				loaderFloor/time.Millisecond, (loaderFloor+loaderJitter)/time.Millisecond)
		}
	}
	// Страница собиралась дольше любого разброса — держать нечего.
	if got := loaderMs(loaderFloor + loaderJitter + time.Second); got != 0 {
		t.Errorf("после долгого сбора loaderMs = %d, ждали 0", got)
	}
}
