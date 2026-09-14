package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// loginsPage — журнал входов и сводки по нему. Рутовая страница: по ней
// видно, откуда ходят все, и раздавать это по лестнице ролей мы не хотим.
func (s *Server) loginsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := repo.LoginFilter{FailsOnly: q.Get("fails") != ""}

	// Пользователь фильтра — числом; мусор в адресе означает «без фильтра»,
	// а не отказ: строку запроса правят и руками.
	typedUser := strings.TrimSpace(q.Get("user"))
	if id, err := strconv.ParseInt(typedUser, 10, 64); err == nil {
		f.UserID = &id
	} else {
		typedUser = ""
	}

	// Адрес разбираем сами: в запрос он идёт как inet, и непонятная строка
	// была бы отказом базы, а не пустым списком. Не разобрался — фильтра нет,
	// но набранное остаётся в поле: подменить его молча значило бы спрятать
	// опечатку.
	typedIP := strings.TrimSpace(q.Get("ip"))
	if place, err := domain.ParseLoginPlace(typedIP); err == nil {
		f.IP = place.IP
	}

	since := time.Now().Add(-domain.SharedLoginWindow)

	entries, err := s.logins.Recent(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	shared, err := s.logins.SharedAccounts(r.Context(), since, domain.SharedLoginSubnets)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sharedIPs, err := s.logins.SharedIPs(r.Context(), since)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	live, err := s.sessions.Live(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Список для выпадающего фильтра: в журнале человек ищет конкретного,
	// а ников он наизусть не помнит.
	users, err := s.users.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.render(w, r, http.StatusOK, "logins", map[string]any{
		"Entries": entries, "Shared": shared, "SharedIPs": sharedIPs,
		"Live": live, "Users": users,
		"FilterUser": typedUser, "FilterIP": typedIP, "FilterFails": f.FailsOnly,
		"WindowDays": days(domain.SharedLoginWindow),
		"TTLDays":    days(domain.LoginLogTTL),
		"MinSubnets": domain.SharedLoginSubnets,
	})
}

// days — срок в сутках для подписи. Сроки объявлены длительностями,
// а людям их показывают днями.
func days(d time.Duration) int { return int(d.Hours() / 24) }

// subnetsByUser — сколько подсетей у кого за окно. Нужно «Доступам»:
// в строке видно число, а за подробностями идут в журнал.
//
// Считается отдельным запросом, а не вместе с пользователем: сессия читает
// пользователя на каждый запрос, и месячному подсчёту там не место.
func (s *Server) subnetsByUser(r *http.Request) map[int64]int {
	// Карта возвращается непустой всегда: шаблон берёт из неё число
	// по ключу, а index по нетипизированному nil он не переживёт.
	if s.logins == nil || !currentUser(r).IsRoot() {
		return map[int64]int{}
	}
	subnets, err := s.logins.SubnetsByUser(r.Context(), time.Now().Add(-domain.SharedLoginWindow))
	if err != nil {
		// Страница доступов не о входах: без этого числа она полезна
		// по-прежнему, а отказ базы уйдёт в лог.
		s.log.Error("не посчитаны подсети входов", "err", err)
		return map[int64]int{}
	}
	return subnets
}
