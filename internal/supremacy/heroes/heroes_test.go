package heroes

import "testing"

// Вшитый справочник — часть образа, и сломать его можно молча: файл
// правится генератором, а читают его страницы. Тест держит форму.
func TestBuiltinSnapshot(t *testing.T) {
	snap, err := Builtin()
	if err != nil {
		t.Fatalf("вшитый справочник не прочитался: %v", err)
	}
	if len(snap.Heroes) < 15 {
		t.Fatalf("героев в справочнике %d — их в игре больше", len(snap.Heroes))
	}
	if snap.Client == "" || snap.CollectedAt.IsZero() {
		t.Errorf("снимок без пометок о происхождении: клиент %q, собран %v", snap.Client, snap.CollectedAt)
	}

	for _, h := range snap.Heroes {
		if h.Identifier == "" || h.UnitTypeID == 0 || h.ShardItemID == 0 {
			t.Errorf("герой без ключей: %+v", h)
		}
		if h.ShortRu == "" && h.ShortEn == "" {
			t.Errorf("герой %s без имени", h.Identifier)
		}
		// Портрет должен быть вшит: справочник без лиц показывать нечем,
		// а имена файлов расходятся с идентификаторами незаметно.
		if _, ok := BuiltinImage(h.Image); !ok {
			t.Errorf("герой %s: нет портрета %q", h.Identifier, h.Image)
		}
	}
}

// Мейв — единственная, чей навык админка жмёт сама, и цифры её уровней
// должны быть на месте: по ним видно, сколько пехоты приведёт призыв.
func TestBuiltinMaeve(t *testing.T) {
	snap, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	maeve, ok := snap.ByUnitType(50617)
	if !ok {
		t.Fatal("Мейв (50617) в справочнике нет")
	}
	if len(maeve.Levels) == 0 {
		t.Fatal("у Мейв нет уровней")
	}

	var deploy *Deploy
	for _, l := range maeve.Levels {
		if l.Level != 1 {
			continue
		}
		if l.Shards == 0 {
			t.Error("первый уровень Мейв ничего не стоит — цена уровней не собралась")
		}
		for _, e := range l.Effects {
			if e.Deploy != nil {
				deploy = e.Deploy
			}
		}
	}
	if deploy == nil {
		t.Fatal("на первом уровне Мейв нет призыва пехоты")
	}
	if deploy.Amount <= 0 || deploy.TimeSec <= 0 {
		t.Errorf("призыв без чисел: %+v", deploy)
	}
}

// Портрет просят из адреса, и подниматься по каталогам через него нельзя.
func TestBuiltinImagePath(t *testing.T) {
	if _, ok := BuiltinImage("../heroes.json"); ok {
		t.Error("по имени с путём отдался файл вне каталога портретов")
	}
	if _, ok := BuiltinImage("нет-такого.png"); ok {
		t.Error("нашёлся портрет, которого нет")
	}
}
