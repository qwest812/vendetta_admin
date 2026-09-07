package supremacy

import (
	"strconv"
	"testing"
	"time"
)

// Срок годности копии — полтора игровых часа: в быстрой партии игровое время
// идёт быстрее, и держать копию столько же было бы враньём.
func TestStateFreshBySpeed(t *testing.T) {
	c := &Client{}
	c.noteSpeeds(
		Game{GameID: "1", TimeScale: 1},
		Game{GameID: "4", TimeScale: 0.25},
		Game{GameID: "10", TimeScale: 0.1},
	)

	cases := []struct {
		game string
		root bool
		want time.Duration
	}{
		{game: "1", want: 90 * time.Minute},
		{game: "4", want: 22*time.Minute + 30*time.Second},
		{game: "10", want: 9 * time.Minute},
		// Партия, которой мы ещё не видели: берём самый короткий срок —
		// лишний поход дешевле вчерашней карты, выданной за сегодняшнюю.
		{game: "неизвестная", want: stateFreshUnknown},
		// Рут смотрит свежее — но не дольше общего срока: в x10 ему
		// достаётся тот же, что и всем.
		{game: "1", root: true, want: 15 * time.Minute},
		{game: "10", root: true, want: 9 * time.Minute},
	}
	for _, c2 := range cases {
		if got := c.StateFresh(c2.game, c2.root); got != c2.want {
			t.Errorf("StateFresh(%q, рут=%v) = %v, ждали %v", c2.game, c2.root, got, c2.want)
		}
	}
}

// Копия общая: кто бы её ни привёз, следующий спрашивающий получает готовое,
// пока она свежая, и не получает, когда протухла.
func TestFreshState(t *testing.T) {
	c := &Client{}
	state := &GameState{GameID: "100", Day: 7}
	c.cacheState("100", state)

	got, at, ok := c.freshState("100", time.Hour)
	if !ok || got != state {
		t.Fatalf("свежую копию не отдали: ok=%v", ok)
	}
	if time.Since(at) > time.Minute {
		t.Errorf("время съёмки = %v, ждали «только что»", at)
	}

	// Состарим копию руками: ждать полтора часа ради проверки полутора часов
	// невозможно.
	c.states["100"].at = time.Now().Add(-2 * time.Hour)
	if _, _, ok := c.freshState("100", time.Hour); ok {
		t.Error("протухшую копию отдали за свежую")
	}
	if _, _, ok := c.freshState("200", time.Hour); ok {
		t.Error("копия партии, которой в кэше нет, взялась из ниоткуда")
	}
}

// Кэш не должен расти вечно: сборщик коалиций приносит по пять партий каждые
// десять минут. Уходит та, к которой дольше всех не обращались.
func TestStateCacheEvicts(t *testing.T) {
	c := &Client{}
	for i := 0; i < stateCacheMax+5; i++ {
		id := strconv.Itoa(i)
		c.cacheState(id, &GameState{GameID: id})
		// Первую партию продолжаем спрашивать: её выбрасывать нельзя,
		// её смотрят люди.
		c.freshState("0", time.Hour)
	}

	if len(c.states) > stateCacheMax {
		t.Errorf("в кэше %d партий, потолок %d", len(c.states), stateCacheMax)
	}
	if _, ok := c.states["0"]; !ok {
		t.Error("партию, которую спрашивали чаще всех, выбросили")
	}
	last := strconv.Itoa(stateCacheMax + 4)
	if _, ok := c.states[last]; !ok {
		t.Errorf("последнюю привезённую партию %s выбросили", last)
	}
}
