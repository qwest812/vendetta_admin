package web

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
)

// Ключи сообщений выглядят по-разному в шаблоне и в коде: {{t "nav.search"}}
// против lang.T("games.failed", err). Общее у них одно — ключ идёт первым
// строковым литералом.
var (
	// Внутри {{…}} вызов перевода встречается в трёх видах: сам по себе,
	// аргументом — (t "clan.empty") — и с присваиванием: {{$yes := t "…"}}.
	// Поэтому сначала вырезаем действия шаблона, а ключи ищем уже внутри них.
	reTmplAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	reTmplKey    = regexp.MustCompile(`\bt\s+"([a-z][a-z0-9_.]*)"`)
	reGoKey      = regexp.MustCompile(`\bT\("([a-z][a-z0-9_.]*)"`)
	// Ошибки предметной области носят ключ прямо в значении: MsgError{"…"}.
	reDomainKey = regexp.MustCompile(`MsgError\{"([a-z][a-z0-9_.]*)"`)
)

// computed — ключи, которые собираются на ходу из кода предметной области:
// «role.» + roleCode и так далее. Литерала такого ключа в исходниках нет,
// поэтому искать их поиском бесполезно — за полноту отвечает отдельный тест.
var computed = []string{"role.", "clanstatus.", "traitkind.", "plan."}

func isComputed(key string) bool {
	for _, p := range computed {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// usedKeys — все ключи, которые где-либо спрашивают у словаря, и где именно.
func usedKeys(t *testing.T) map[string][]string {
	t.Helper()

	used := map[string][]string{}
	collect := func(name, body string, re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			// «role.» и подобное — не ключ, а его начало: дальше в коде
			// идёт код роли. Полноту таких ключей проверяет другой тест.
			if strings.HasSuffix(m[1], ".") {
				continue
			}
			used[m[1]] = append(used[m[1]], name)
		}
	}

	names, err := fs.Glob(templatesFS, "templates/*.gohtml")
	if err != nil {
		t.Fatalf("шаблоны: %v", err)
	}
	for _, name := range names {
		body, err := fs.ReadFile(templatesFS, name)
		if err != nil {
			t.Fatalf("чтение %s: %v", name, err)
		}
		for _, action := range reTmplAction.FindAllString(string(body), -1) {
			collect(filepath.Base(name), action, reTmplKey)
		}
	}

	// Сообщения из обработчиков — та же половина интерфейса: ошибки формы
	// и подтверждения человек читает на том же языке, что и подписи.
	scan := func(pattern string, re *regexp.Regexp) {
		sources, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("исходники %s: %v", pattern, err)
		}
		for _, name := range sources {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			body, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("чтение %s: %v", name, err)
			}
			collect(filepath.Base(name), string(body), re)
		}
	}
	scan("*.go", reGoKey)
	// Ошибки domain тоже видит человек: их ключи объявлены там же, где сами
	// ошибки, и без этого выглядели бы неиспользуемыми.
	scan("../domain/*.go", reDomainKey)
	// Слова личных списков лежат в relationWords значениями полей, а не
	// литералами в t(...): поиском такие не находятся, зато находятся
	// отражением — и тогда новое поле не забудут перевести.
	for _, w := range []relationWords{enemyWords, friendWords} {
		v := reflect.ValueOf(w)
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			// Path — кусок адреса, Class — имя css-класса: не переводятся.
			if name == "Path" || name == "Class" {
				continue
			}
			used[v.Field(i).String()] = append(used[v.Field(i).String()], "relationWords."+name)
		}
	}
	return used
}

// Опечатка в ключе не ломает страницу — на ней просто виден сам ключ.
// Поэтому её и надо ловить тестом: глазами такое замечают последним.
func TestAllUsedKeysExist(t *testing.T) {
	var missing []string
	for key, where := range usedKeys(t) {
		if !i18n.Has(key) {
			missing = append(missing, key+" — "+strings.Join(where, ", "))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("ключей нет в словаре (%d):\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

// Английский обязателен: без него страница молча покажет русский текст,
// и «перевод готов» окажется неправдой.
func TestEverythingIsTranslated(t *testing.T) {
	if left := i18n.Untranslated(); len(left) > 0 {
		t.Errorf("без английского осталось %d ключей:\n%s", len(left), strings.Join(left, "\n"))
	}
}

// Ключ, которого никто не спрашивает, — мёртвый груз: его правят зря
// и по нему судят о переводе неверно.
func TestNoUnusedKeys(t *testing.T) {
	used := usedKeys(t)
	var dead []string
	for _, key := range i18n.Keys() {
		if isComputed(key) {
			continue
		}
		if _, ok := used[key]; !ok {
			dead = append(dead, key)
		}
	}
	if len(dead) > 0 {
		t.Errorf("ключи из словаря нигде не используются (%d):\n%s", len(dead), strings.Join(dead, "\n"))
	}
}

// Ключи, собираемые из кодов предметной области, поиском не находятся —
// значит, перечислить их надо руками. Новая роль или статус клана без
// подписи иначе доедет до страницы в виде «clanstatus.foo».
func TestComputedKeysCoverDomain(t *testing.T) {
	var want []string
	for _, r := range []domain.Role{domain.RoleUser, domain.RoleAdmin, domain.RoleRoot} {
		want = append(want, "role."+string(r))
	}
	for _, c := range domain.ClanStatuses {
		want = append(want, "clanstatus."+string(c))
	}
	// Знаки признаков domain отдаёт готовыми ключами — теми же, что здесь.
	for _, k := range domain.TraitKinds {
		want = append(want, k.TitleKey())
	}
	// Пакеты доступа: новый пакет без подписи доехал бы до «Доступов»
	// в виде «plan.foo» — и рут раздавал бы его вслепую.
	for _, p := range domain.Plans {
		want = append(want, p.TitleKey())
	}
	for _, key := range want {
		if !i18n.Has(key) {
			t.Errorf("нет подписи для %s", key)
		}
	}
}

// Английский набор шаблонов должен не просто разбираться, а показывать
// английские слова: если ключ где-то остался литералом по-русски, это
// видно только на отрисованной странице.
func TestPageRendersInEnglish(t *testing.T) {
	pages, err := parseTemplates(i18n.EN)
	if err != nil {
		t.Fatalf("разбор шаблонов: %v", err)
	}

	var buf bytes.Buffer
	err = pages["faq"].ExecuteTemplate(&buf, "base.gohtml", map[string]any{
		"CurrentUser": &domain.User{ID: 1, Nickname: "root", Role: domain.RoleRoot},
		"CSRFToken":   "csrf", "Path": "/faq", "Lang": i18n.EN, "Back": "/faq",
	})
	if err != nil {
		t.Fatalf("отрисовка: %v", err)
	}

	page := buf.String()
	for _, want := range []string{
		`<html lang="en">`,
		">Search<", ">Clans<", ">Help<", ">Log out<", // меню и шапка
		"Root",                       // роль подписана словом, а не кодом
		"How to use the admin panel", // текст самой страницы
		// Переключатель ведёт обратно на эту же страницу; адрес шаблон
		// экранирует сам, а разбирает его обратно уже обработчик.
		`href="/lang/ru?back=%2ffaq"`, ">RU<",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("на английской странице нет %q", want)
		}
	}
	// Кириллица на английской странице означает забытый литерал. Сам
	// переключатель — исключение: язык там называет себя сам, и «Русский»
	// в подсказке к кнопке стоит по делу.
	body := regexp.MustCompile(`(?s)<a class="lang".*?</a>`).ReplaceAllString(page, "")
	if m := regexp.MustCompile(`[А-Яа-яЁё]+`).FindString(body); m != "" {
		t.Errorf("на английской странице осталось русское слово: %q", m)
	}
}
