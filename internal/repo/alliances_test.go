package repo

import (
	"testing"

	"Vendetta_admin/internal/domain"
)

// Повтор в списке — не выдумка: один и тот же человек приходит и из состава
// партии, и из состава клана. Построчная запись такое переживала, а пакетная
// упала бы целиком: ON CONFLICT DO UPDATE не правит одну строку дважды
// в одном запросе.
func TestAllianceColumnsDropsDuplicates(t *testing.T) {
	users, ids, names, tags := allianceColumns([]domain.Alliance{
		{SiteUserID: "1", ID: "10", Name: "Волки", Tag: "WLF"},
		{SiteUserID: "2", ID: "", Name: "", Tag: ""},
		// Тот же человек второй раз, уже с другим кланом: побеждает поздний,
		// он и есть свежий.
		{SiteUserID: "1", ID: "20", Name: "Медведи", Tag: "BER"},
		// Без номера на сайте писать нечего.
		{SiteUserID: "", ID: "30", Name: "Никто"},
	})

	if len(users) != 2 {
		t.Fatalf("строк = %d, ожидалось 2: %v", len(users), users)
	}
	if users[0] != "1" || ids[0] != "20" || names[0] != "Медведи" || tags[0] != "BER" {
		t.Errorf("первая строка = %s/%s/%s/%s", users[0], ids[0], names[0], tags[0])
	}
	if users[1] != "2" || ids[1] != "" {
		t.Errorf("вторая строка = %s/%s", users[1], ids[1])
	}
	if len(ids) != 2 || len(names) != 2 || len(tags) != 2 {
		t.Errorf("столбцы разной длины: %d/%d/%d/%d", len(users), len(ids), len(names), len(tags))
	}
}

// Пустой список не должен превращаться в запрос: unnest от пустых массивов
// вставил бы ноль строк, но обращение к базе всё равно случилось бы.
func TestAllianceColumnsEmpty(t *testing.T) {
	users, _, _, _ := allianceColumns(nil)
	if len(users) != 0 {
		t.Errorf("из пустого списка вышло %d строк", len(users))
	}
}
