package repo

import (
	"slices"
	"testing"
)

// Коды признаков приходят из адреса, а там одна и та же галочка легко
// оказывается дважды: две вкладки, склеенная ссылка, ручная правка строки.
// Условие поиска сверяет число совпадений с числом кодов, поэтому повтор
// без чистки означал бы «не найдено никого» — молча и необъяснимо.
func TestDistinctKeepsFirstOrder(t *testing.T) {
	got := distinct([]string{"lies", "multiaccount", "lies", "night_player", "multiaccount"})
	want := []string{"lies", "multiaccount", "night_player"}
	if !slices.Equal(got, want) {
		t.Errorf("distinct = %v, ожидалось %v", got, want)
	}
	if got := distinct(nil); got != nil {
		t.Errorf("пустой список должен остаться пустым, получено %v", got)
	}
}
