package heroes

import (
	"reflect"
	"testing"
)

// Кусок бандла клиента: минифицированный, с теми же именами полей и теми же
// подменёнными однобуквенными переменными, что в настоящем. Держать его
// рядом важнее, чем красиво: разбор мы ведём по форме, а не по смыслу,
// и меняется она без предупреждения.
const bundleSample = `var o={` +
	`[a.I.ViscountAllenby]:{heroUnitClassIdentifier:n._.Cavalry,shardItemId:23132,unitTypeId:22277,rarity:r.Y.Epic},` +
	`[a.I.FionaMaevePorter]:{heroUnitClassIdentifier:n._.Infantry,shardItemId:50655,unitTypeId:50617,rarity:r.Y.Epic}}` +
	`;S.s2.ModdableI18n.getHeroShortName=e=>{var{i18n:t}=S.s2;switch(e){` +
	`case fh.I.ViscountAllenby:return t.gettext("Allenby");` +
	`case fh.I.FionaMaevePorter:return t.gettext("Maeve");default:return""}};` +
	`S.s2.ModdableI18n.getHeroSubTitle=e=>{var{i18n:t}=S.s2;switch(e){` +
	`case fh.I.FionaMaevePorter:return t.gettext("Expert Recruiter");default:return""}};` +
	`S.s2.ModdableI18n.getModifierSetDescription=(e,t)=>{var{i18n:i}=S.s2;switch(e){` +
	`case vh.J.InfantryDeployAmountActive:return i.gettext('Maeve puts her in a "deploying" state for %s hours.',x);` +
	`case vh.J.FionaViewBuffOwn:return i.gettext("Maeve\'s view range increases by %s.",y);default:return""}};` +
	`var n=function(e){return e.CavalryAttackBuff="cav_atk_buff",` +
	`e.InfantryDeployAmountActive="inf_deploy_amount_active",e.FionaViewBuffOwn="fio_view_buff_own",e}({})`

func TestParseHeroConfigs(t *testing.T) {
	got := parseHeroConfigs(bundleSample)
	want := []heroConfig{
		{Identifier: "ViscountAllenby", Class: "Cavalry", ShardItemID: 23132, UnitTypeID: 22277, Rarity: "Epic"},
		{Identifier: "FionaMaevePorter", Class: "Infantry", ShardItemID: 50655, UnitTypeID: 50617, Rarity: "Epic"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("герои разобрались не так:\n got %+v\nwant %+v", got, want)
	}
}

// Функции в бандле идут подряд и устроены одинаково: разбор одной не должен
// утаскивать ветки соседней.
func TestParseHeroTextsStopsAtOwnSwitch(t *testing.T) {
	short := parseHeroTexts(bundleSample, "getHeroShortName")
	if short["ViscountAllenby"] != "Allenby" || short["FionaMaevePorter"] != "Maeve" {
		t.Errorf("короткие имена: %v", short)
	}
	sub := parseHeroTexts(bundleSample, "getHeroSubTitle")
	if len(sub) != 1 || sub["FionaMaevePorter"] != "Expert Recruiter" {
		t.Errorf("подписи разобрались с чужими ветками: %v", sub)
	}
}

// В описаниях умений встречаются обе кавычки и экранирование внутри:
// у Мейв в тексте закавычено слово, у неё же — апостроф.
func TestParseModifierTexts(t *testing.T) {
	got := parseModifierTexts(bundleSample, "getModifierSetDescription")
	want := map[string]string{
		"InfantryDeployAmountActive": `Maeve puts her in a "deploying" state for %s hours.`,
		"FionaViewBuffOwn":           `Maeve's view range increases by %s.`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("описания умений:\n got %q\nwant %q", got, want)
	}
}

func TestParseGroupIDs(t *testing.T) {
	got := parseGroupIDs(bundleSample)
	if got["inf_deploy_amount_active"] != "InfantryDeployAmountActive" {
		t.Errorf("умение не сошлось с текстом: %v", got)
	}
}

func TestJSString(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`"простой"`, "простой", true},
		{`'с "кавычками" внутри'`, `с "кавычками" внутри`, true},
		{`"экранированная \" кавычка"`, `экранированная " кавычка`, true},
		{`"перенос\nстроки"`, "перенос\nстроки", true},
		{`не строка`, "", false},
		{`"незакрытая`, "", false},
	}
	for _, c := range cases {
		got, ok := jsString(c.in, 0)
		if got != c.want || ok != c.ok {
			t.Errorf("jsString(%q) = %q, %v; хотели %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Герой узнаётся по осколку, которым за него платят: имя оффера — текст
// для человека, и опираться на него нельзя.
func TestLevelsFor(t *testing.T) {
	offers := []Offer{
		{Name: "(Recruit) Fiona 'Maeve' Porter - unlock", Items: []int{50656, 50657}, Shards: map[int]int{50655: 50}},
		{Name: "(Recruit) Fiona 'Maeve' Porter - Promote lvl 2", Items: []int{50658}, Shards: map[int]int{50655: 70}},
		{Name: "(Recruit) John Pershing - Promote lvl 2", Items: []int{50664}, Shards: map[int]int{50663: 90}},
	}
	got := levelsFor(50655, offers)
	want := []Level{
		{Level: 1, Shards: 50, Items: []int{50656, 50657}},
		{Level: 2, Shards: 70, Items: []int{50658}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("лестница уровней:\n got %+v\nwant %+v", got, want)
	}
}

// Витрина отдаёт ступени вперемешку, а читается лестница по порядку.
// Пропуск в середине сохраняем как есть: потерять из-за него весь хвост
// было бы хуже, чем показать дыру.
func TestLevelsForOrder(t *testing.T) {
	offers := []Offer{
		{Name: "Promote lvl 3", Shards: map[int]int{1: 90}},
		{Name: "unlock", Shards: map[int]int{1: 50}},
		{Name: "Promote lvl 2", Shards: map[int]int{1: 70}},
	}
	got := levelsFor(1, offers)
	if len(got) != 3 {
		t.Fatalf("ступеней %d: %+v", len(got), got)
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].Level != want {
			t.Errorf("ступень %d на месте %d", got[i].Level, i)
		}
	}
}

// Цифры умений приходят только из партии, поэтому при обновлении их
// переносят из прошлого снимка — по номеру ступени, а не по цене.
func TestCopyEffects(t *testing.T) {
	old := Hero{Levels: []Level{
		{Level: 1, Shards: 50, Effects: []Effect{{Set: "lvl1", SpeedFactor: 0.25}}},
		{Level: 2, Shards: 70, Effects: []Effect{{Set: "lvl2", VisionFactor: 0.2}}},
	}}
	fresh := []Level{{Level: 1, Shards: 60}, {Level: 2, Shards: 80}, {Level: 3, Shards: 100}}

	copyEffects(old, fresh)

	if len(fresh[0].Effects) != 1 || fresh[0].Effects[0].Set != "lvl1" {
		t.Errorf("первый уровень: %+v", fresh[0])
	}
	if fresh[0].Shards != 60 {
		t.Errorf("цена перетёрлась старой: %d", fresh[0].Shards)
	}
	if len(fresh[2].Effects) != 0 {
		t.Errorf("у нового уровня взялись чужие цифры: %+v", fresh[2])
	}
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"FionaMaevePorter": "fiona_maeve_porter",
		"Gibraltar":        "gibraltar",
		"TogoHeihachiro":   "togo_heihachiro",
	}
	for in, want := range cases {
		if got := snakeCase(in); got != want {
			t.Errorf("snakeCase(%q) = %q, хотели %q", in, got, want)
		}
	}
}

// Под пустым ключом словарь игры держит свои служебные поля объектом —
// разбор не должен об это спотыкаться.
func TestParseDict(t *testing.T) {
	raw := []byte(`{"domain":"s1914","locale_data":{"s1914":{` +
		`"":{"lang":"ru","plural_forms":"nplurals=3"},` +
		`"Maeve":["Мейв"],"York":[""]}}}`)
	d, err := parseDict(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.tr("Maeve"); got != "Мейв" {
		t.Errorf("перевод: %q", got)
	}
	// Пустой перевод — не перевод: интерфейс сам решит, показывать ли
	// вместо него оригинал.
	if got := d.tr("York"); got != "" {
		t.Errorf("пустой перевод отдался как настоящий: %q", got)
	}
	if got := d.tr("Pershing"); got != "" {
		t.Errorf("неизвестное слово: %q", got)
	}
}
