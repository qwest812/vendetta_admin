package repo

import (
	"strings"
	"testing"

	"Vendetta_admin/internal/domain"
)

// Запас карточек общий на врагов и друзей, поэтому выражение «сколько
// занято» обязано считать обе таблицы. Забыть одну — значит выдать человеку
// вдвое больше обещанного, и снаружи это никак не видно.
func TestUsedSQLCountsBothLists(t *testing.T) {
	sql := usedSQL()
	for _, table := range relationTables {
		if !strings.Contains(sql, "FROM "+table+" ") {
			t.Errorf("в счёте нет таблицы %s: %s", table, sql)
		}
	}
	if n := strings.Count(sql, "count(*)"); n != len(relationTables) {
		t.Errorf("слагаемых %d, списков %d: %s", n, len(relationTables), sql)
	}
	// Пользователь приходит параметром, а не подстановкой в текст запроса.
	if !strings.Contains(sql, "user_id = $1") {
		t.Errorf("счёт не привязан к $1: %s", sql)
	}
}

// Колонки пользователя и приёмники под них — два списка, которые обязаны
// совпадать. Разъехавшись, они не ломают сборку: лишняя колонка роняет
// запрос в рантайме, а недостающая молча оставляет поле пустым. Так пакет
// доступа однажды и не доехал до сессии.
func TestUserColumnsMatchScanTargets(t *testing.T) {
	var u domain.User
	var email *string
	cols := strings.Split(userColumns, ", ")
	if got, want := len(userScanTargets(&u, &email)), len(cols); got != want {
		t.Errorf("приёмников %d, колонок %d: %v", got, want, cols)
	}
}

// Сессия читает пользователя join-ом, поэтому колонки ей нужны с именем
// таблицы — но ровно те же и в том же порядке.
func TestUserColumnsOfKeepsOrder(t *testing.T) {
	cols := strings.Split(userColumns, ", ")
	aliased := strings.Split(userColumnsOf("u"), ", ")
	if len(aliased) != len(cols) {
		t.Fatalf("колонок с префиксом %d, без него %d", len(aliased), len(cols))
	}
	for i := range cols {
		if aliased[i] != "u."+cols[i] {
			t.Errorf("колонка %d: %q, ожидалась %q", i, aliased[i], "u."+cols[i])
		}
	}
}
