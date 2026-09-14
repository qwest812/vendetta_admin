package repo

import (
	"strings"
	"testing"
)

// Метка «вход из нескольких мест» считается только по удачным входам.
// Иначе достаточно было бы перебирать чужой ник, чтобы пометить его
// владельца, — то есть метку мог бы навесить кто угодно снаружи.
func TestSubnetsCountOnlySuccessfulLogins(t *testing.T) {
	sql := subnetsByUserSQL()
	if !strings.Contains(sql, "WHERE ok") {
		t.Errorf("в счёт подсетей попадают неудачные входы: %s", sql)
	}
	if !strings.Contains(sql, "count(DISTINCT subnet)") {
		t.Errorf("считаются не подсети: %s", sql)
	}
	// Записи без пользователя — попытки на несуществующий логин, и в счёт
	// по людям им не место.
	if !strings.Contains(sql, "user_id IS NOT NULL") {
		t.Errorf("в счёт попадают входы без пользователя: %s", sql)
	}
}

// Пустой фильтр по адресу не должен ронять запрос: ”::inet — отказ типа,
// а порядок вычисления в OR не обещан, поэтому «слева пусто» от него
// не спасает. Спасает nullif.
func TestRecentIPFilterSurvivesEmptyValue(t *testing.T) {
	sql := recentSQL()
	if strings.Count(sql, "nullif($2, '')::inet") != 1 {
		t.Errorf("адрес приводится к inet без nullif: %s", sql)
	}
	if strings.Contains(sql, "= $2::inet") {
		t.Errorf("остался прямой привод пустой строки к inet: %s", sql)
	}
}
