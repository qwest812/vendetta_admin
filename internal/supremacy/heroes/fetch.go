package heroes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Откуда берётся справочник. Всё это открытая статика игры: ни подписи,
// ни захода в партию она не требует — клиент игры её раздаёт всем подряд.
const (
	clientBase = "https://www.supremacy1914.com/clients/s1914-client-mobile/s1914-client-mobile_live"
	ruDictURL  = "https://static.supremacy1914.com/fileadmin/i18n/s1914/ru.json"
	// userAgent — cloudflare перед статикой отвечает 403 на запрос без него.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	// fetchTimeout — на весь сбор: файлов десятка два, самый большой
	// бандл под полтора мегабайта.
	fetchTimeout = 3 * time.Minute
)

// Offer — предложение магазина игры про героя: «разблокировать» или
// «повысить до уровня N». Цена в осколках, а осколок у каждого героя свой,
// поэтому по нему герой и опознаётся.
type Offer struct {
	Name string
	// Items — что игра выдаёт за покупку. Ими же эффекты привязаны
	// к уровню в данных партии.
	Items []int
	// Shards — сколько и каких осколков стоит. Ключ — номер предмета.
	Shards map[int]int
}

// Fetch собирает свежий справочник: состав, имена, тексты умений и цены
// уровней. Всё это лежит в открытом доступе — в бандле клиента, в словаре
// локализации и в ответе магазина, — так что заходить в партию не нужно.
//
// Чего здесь нет, так это цифр бафов: «+25% скорости на втором уровне»
// приходит только с игрового сервера, в справочнике типов юнитов партии.
// Поэтому эффекты переносятся из прошлого снимка (prev), а у героя, которого
// в нём не было, уровни остаются без цифр — с ценой, но без эффектов.
//
// Второе возвращаемое значение — портреты: имя файла и его содержимое.
func Fetch(ctx context.Context, hc *http.Client, offers []Offer, prev *Snapshot) (*Snapshot, map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	if hc == nil {
		hc = http.DefaultClient
	}

	index, err := get(ctx, hc, clientBase+"/index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("страница клиента игры: %w", err)
	}
	bundle := appBundleRe.FindSubmatch(index)
	if bundle == nil {
		return nil, nil, fmt.Errorf("в странице клиента игры не нашлось app.js")
	}
	appName := string(bundle[1])

	app, err := get(ctx, hc, clientBase+"/"+appName)
	if err != nil {
		return nil, nil, fmt.Errorf("бандл клиента %s: %w", appName, err)
	}
	js := string(app)

	dict, err := get(ctx, hc, ruDictURL)
	if err != nil {
		return nil, nil, fmt.Errorf("словарь локализации: %w", err)
	}
	ru, err := parseDict(dict)
	if err != nil {
		return nil, nil, err
	}

	configs := parseHeroConfigs(js)
	if len(configs) == 0 {
		return nil, nil, fmt.Errorf("в бандле %s не нашлось ни одного героя", appName)
	}

	var (
		short     = parseHeroTexts(js, "getHeroShortName")
		subtitle  = parseHeroTexts(js, "getHeroSubTitle")
		groups    = parseGroupIDs(js)
		skillName = parseModifierTexts(js, "getModifierSetName")
		buffName  = parseModifierTexts(js, "getModifierSetBuffName")
		skillText = parseModifierTexts(js, "getModifierSetDescription")
	)

	snap := &Snapshot{
		Version:     1,
		CollectedAt: time.Now().UTC().Truncate(time.Second),
		Client:      appName,
		Heroes:      make([]Hero, 0, len(configs)),
	}
	for _, c := range configs {
		h := Hero{
			Identifier: c.Identifier, UnitTypeID: c.UnitTypeID, ShardItemID: c.ShardItemID,
			Class: c.Class, Rarity: c.Rarity, Image: snakeCase(c.Identifier) + ".png",
			ShortEn: short[c.Identifier], SubtitleEn: subtitle[c.Identifier],
		}
		h.ShortRu = ru.tr(h.ShortEn)
		h.SubtitleRu = ru.tr(h.SubtitleEn)

		// Прошлый снимок отдаёт то, чего в открытых источниках нет:
		// полное имя героя и цифры бафов. Оба приходят из партии.
		old, known := prevHero(prev, c.UnitTypeID)
		if known {
			h.NameEn, h.NameRu = old.NameEn, old.NameRu
			if old.Image != "" {
				h.Image = old.Image
			}
		}

		h.Levels = levelsFor(c.ShardItemID, offers)
		if known {
			copyEffects(old, h.Levels)
			// Умения берём из прошлого снимка: их состав виден по эффектам,
			// а эффекты — из партии. Тексты при этом обновляем: они как раз
			// из бандла и могли смениться.
			h.Skills = make([]Skill, 0, len(old.Skills))
			for _, s := range old.Skills {
				h.Skills = append(h.Skills, skillFor(s.GroupID, groups, skillName, buffName, skillText, ru))
			}
		}
		snap.Heroes = append(snap.Heroes, h)
	}
	// По номеру типа юнита: в бандле герои лежат в порядке, в каком их
	// когда-то дописывали, и от выпуска к выпуску он гуляет. Свой порядок
	// заодно ставит новых героев в конец — они и появились последними.
	sort.Slice(snap.Heroes, func(i, j int) bool {
		return snap.Heroes[i].UnitTypeID < snap.Heroes[j].UnitTypeID
	})

	images := make(map[string][]byte, len(snap.Heroes))
	for _, h := range snap.Heroes {
		raw, err := get(ctx, hc, clientBase+"/images/heroes/unit/"+h.Image)
		if err != nil {
			// Портрет — не повод ронять весь сбор: у нового героя файл
			// может называться иначе, чем его идентификатор, и тогда
			// в справочнике останется старая картинка или пусто.
			continue
		}
		images[h.Image] = raw
	}
	return snap, images, nil
}

// appBundleRe — имя бандла в странице клиента: оно меняется с каждым
// выпуском игры, поэтому и читается оттуда, а не зашивается.
var appBundleRe = regexp.MustCompile(`src="(app\.\w+\.js)"`)

// heroConfigRe — карта героев в бандле. Выглядит она так:
//
//	[a.I.FionaMaevePorter]:{heroUnitClassIdentifier:n._.Infantry,
//	 shardItemId:50655,unitTypeId:50617,rarity:r.Y.Epic}
//
// Имена переменных минификатор меняет от выпуска к выпуску, поэтому они
// и записаны как \w+: держимся за поля, а не за буквы.
var heroConfigRe = regexp.MustCompile(
	`\[\w+\.I\.(\w+)\]:\{heroUnitClassIdentifier:\w+\._\.(\w+),shardItemId:(\d+),unitTypeId:(\d+),rarity:\w+\.Y\.(\w+)\}`)

type heroConfig struct {
	Identifier  string
	Class       string
	ShardItemID int
	UnitTypeID  int
	Rarity      string
}

func parseHeroConfigs(js string) []heroConfig {
	out := make([]heroConfig, 0, 24)
	for _, m := range heroConfigRe.FindAllStringSubmatch(js, -1) {
		shard, _ := strconv.Atoi(m[3])
		unit, _ := strconv.Atoi(m[4])
		out = append(out, heroConfig{
			Identifier: m[1], Class: m[2], ShardItemID: shard, UnitTypeID: unit, Rarity: m[5],
		})
	}
	return out
}

// heroCaseRe и modifierCaseRe — ветки switch в переводимых функциях клиента:
//
//	case fh.I.FionaMaevePorter:return t.gettext("Maeve")
//
// Сам текст регуляркой не берём: в нём бывают экранированные кавычки, и его
// читает jsString.
var (
	heroCaseRe     = regexp.MustCompile(`case \w+\.I\.(\w+):return \w+\.gettext\(`)
	modifierCaseRe = regexp.MustCompile(`case \w+\.J\.(\w+):return \w+\.gettext\(`)
)

// switchBody вырезает тело одного switch из бандла: от присваивания функции
// до её ветки default. Без границы разбор уехал бы в соседнюю функцию —
// они идут подряд и устроены одинаково.
func switchBody(js, fn string) string {
	start := strings.Index(js, fn+"=")
	if start < 0 {
		return ""
	}
	end := strings.Index(js[start:], `default:return""}`)
	if end < 0 {
		return ""
	}
	return js[start : start+end]
}

// parseHeroTexts собирает тексты, привязанные к герою: короткое имя,
// подпись под портретом.
func parseHeroTexts(js, fn string) map[string]string {
	return parseCases(switchBody(js, fn), heroCaseRe)
}

// parseModifierTexts собирает тексты, привязанные к умению: название,
// подпись эффекта, описание.
func parseModifierTexts(js, fn string) map[string]string {
	return parseCases(switchBody(js, fn), modifierCaseRe)
}

func parseCases(body string, re *regexp.Regexp) map[string]string {
	out := make(map[string]string)
	for _, loc := range re.FindAllStringSubmatchIndex(body, -1) {
		key := body[loc[2]:loc[3]]
		text, ok := jsString(body, loc[1])
		if !ok {
			continue
		}
		out[key] = text
	}
	return out
}

// groupIDRe — перечисление умений в бандле: e.InfantryDeployAmountActive=
// "inf_deploy_amount_active". Слева — ключ, которым подписаны тексты,
// справа — имя, которым умение зовётся в данных партии.
var groupIDRe = regexp.MustCompile(`\.(\w+)="([a-z][a-z0-9_]+)"`)

// parseGroupIDs строит «имя из данных партии → ключ текстов». Перечисление
// ищется от первого известного умения: рядом лежат все остальные.
func parseGroupIDs(js string) map[string]string {
	i := strings.Index(js, `CavalryAttackBuff="`)
	if i < 0 {
		return nil
	}
	from := i - 200
	if from < 0 {
		from = 0
	}
	to := i + 6000
	if to > len(js) {
		to = len(js)
	}
	out := make(map[string]string)
	for _, m := range groupIDRe.FindAllStringSubmatch(js[from:to], -1) {
		out[m[2]] = m[1]
	}
	return out
}

// jsString читает строковый литерал JavaScript, начинающийся с позиции i.
// Кавычки бывают и одинарные, и двойные, внутри встречаются экранированные:
// в описании навыка Мейв, например, есть закавыченное слово.
func jsString(s string, i int) (string, bool) {
	if i >= len(s) || (s[i] != '"' && s[i] != '\'') {
		return "", false
	}
	quote := s[i]
	var b strings.Builder
	for i++; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 >= len(s) {
				return "", false
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(s[i])
			}
		case quote:
			return b.String(), true
		default:
			b.WriteByte(c)
		}
	}
	return "", false
}

// dict — словарь локализации игры. Ключ в нём — английский оригинал,
// значение — список переводов, из которого нужен первый. Значения оставляем
// неразобранными: под пустым ключом словарь держит свои служебные поля
// объектом, и строгий тип на нём спотыкался бы.
type dict map[string]json.RawMessage

func parseDict(raw []byte) (dict, error) {
	var file struct {
		LocaleData map[string]dict `json:"locale_data"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("разбор словаря локализации: %w", err)
	}
	for _, d := range file.LocaleData {
		return d, nil
	}
	return nil, fmt.Errorf("словарь локализации пуст")
}

// tr переводит строку. Нет перевода — отдаём пусто, а не английский:
// интерфейс сам решит, что показывать, и не станет выдавать оригинал
// за перевод.
func (d dict) tr(en string) string {
	if en == "" {
		return ""
	}
	raw, ok := d[en]
	if !ok {
		return ""
	}
	var vals []string
	if err := json.Unmarshal(raw, &vals); err != nil || len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// levelRe — номер ступени в названии оффера: «… - Promote lvl 11».
// Первая покупка называется «unlock» и номера не имеет — это уровень 1.
var levelRe = regexp.MustCompile(`lvl (\d+)`)

// levelsFor собирает лестницу прокачки героя из предложений магазина.
// Герой узнаётся по осколку, которым за него платят: имя оффера — текст
// для человека, а осколок у каждого героя свой.
func levelsFor(shardItemID int, offers []Offer) []Level {
	byLevel := make(map[int]Level)
	for _, o := range offers {
		price, ok := o.Shards[shardItemID]
		if !ok {
			continue
		}
		level := 1
		if m := levelRe.FindStringSubmatch(o.Name); m != nil {
			level, _ = strconv.Atoi(m[1])
		}
		byLevel[level] = Level{Level: level, Shards: price, Items: o.Items}
	}
	out := make([]Level, 0, len(byLevel))
	for _, l := range byLevel {
		out = append(out, l)
	}
	// По номеру ступени: витрина отдаёт их вперемешку, а лестница читается
	// только по порядку. Пропуск в середине не выбрасываем — уж лучше
	// лестница с дырой, чем молча потерянный хвост.
	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return out
}

func prevHero(prev *Snapshot, unitTypeID int) (Hero, bool) {
	if prev == nil {
		return Hero{}, false
	}
	return prev.ByUnitType(unitTypeID)
}

// copyEffects переносит цифры бафов из прошлого снимка в свежие уровни.
// Сходятся они по номеру ступени: цена ступени могла смениться, а что она
// открывает — нет.
func copyEffects(old Hero, levels []Level) {
	was := make(map[int][]Effect, len(old.Levels))
	for _, l := range old.Levels {
		was[l.Level] = l.Effects
	}
	for i := range levels {
		levels[i].Effects = was[levels[i].Level]
	}
}

func skillFor(groupID string, groups, name, buff, text map[string]string, ru dict) Skill {
	key := groups[groupID]
	s := Skill{
		GroupID: groupID, Key: key,
		NameEn: name[key], BuffEn: buff[key], TextEn: text[key],
	}
	s.NameRu, s.BuffRu, s.TextRu = ru.tr(s.NameEn), ru.tr(s.BuffEn), ru.tr(s.TextEn)
	return s
}

// snakeCase превращает идентификатор героя в имя файла портрета:
// FionaMaevePorter → fiona_maeve_porter. Так их называет сама игра.
func snakeCase(ident string) string {
	var b strings.Builder
	for i, r := range ident {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func get(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
