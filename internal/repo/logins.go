package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// loginLimit — сколько записей показывает журнал входов. Столько же,
// сколько в журнале действий: страница должна оставаться одной страницей.
const loginLimit = 200

// Logins — журнал входов: кто, откуда и получилось ли. Отдельно
// от audit_log: тот про правки данных, и неудачные пароли утопили бы
// его в шуме.
type Logins struct{ pool *pgxpool.Pool }

func NewLogins(pool *pgxpool.Pool) *Logins { return &Logins{pool: pool} }

// LoginEvent — запись журнала. Один тип и на запись, и на чтение: при
// записи ID, Nickname и CreatedAt пусты — их заполняют база и join.
type LoginEvent struct {
	ID int64
	// UserID пуст, когда такого логина в базе нет: попытку мы всё равно
	// пишем, по ней и видно, что чей-то ник перебирают.
	UserID   *int64
	Nickname string
	// Login — что набрали в форме, как есть. Нужен именно набранный:
	// у входов на несуществующий ник другого имени нет.
	Login     string
	IP        string
	Subnet    string
	UserAgent string
	OK        bool
	CreatedAt time.Time
}

// Log пишет попытку входа. Пароль здесь не участвует ни в каком виде.
func (r *Logins) Log(ctx context.Context, e LoginEvent) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO login_events (user_id, login, ip, subnet, user_agent, ok)
		 VALUES ($1, $2, $3::inet, $4::inet, $5, $6)`,
		e.UserID, e.Login, e.IP, e.Subnet, e.UserAgent, e.OK)
	return err
}

// LoginFilter — чем сужают журнал. Пустое поле означает «без фильтра».
type LoginFilter struct {
	UserID *int64
	// IP — уже разобранный адрес. Мусор сюда попадать не должен: в запрос
	// он идёт как inet, и непонятная строка была бы отказом базы,
	// а не пустым списком.
	IP string
	// FailsOnly — только отказы: по ним видно перебор пароля.
	FailsOnly bool
}

// Recent — журнал свежей записью вверх.
func (r *Logins) Recent(ctx context.Context, f LoginFilter) ([]LoginEvent, error) {
	rows, err := r.pool.Query(ctx, recentSQL(), f.UserID, f.IP, f.FailsOnly, loginLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LoginEvent
	for rows.Next() {
		var e LoginEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.Nickname, &e.Login,
			&e.IP, &e.Subnet, &e.UserAgent, &e.OK, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SubnetsByUser — сколько разных подсетей у каждого за срок.
//
// Считаются только удачные входы: неудачные означают, что кто-то не знает
// пароля, и метить ими владельца ника значило бы наказывать его за то,
// что его ник перебирают.
func (r *Logins) SubnetsByUser(ctx context.Context, since time.Time) (map[int64]int, error) {
	rows, err := r.pool.Query(ctx, subnetsByUserSQL(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Запрос вынесен функцией по той же причине, что и у SubnetsByUser:
// в нём спрятано решение, которое легко потерять при правке. Здесь это
// nullif вокруг адреса — см. комментарий внутри.
func recentSQL() string {
	// nullif, а не сравнение с пустой строкой: порядок вычисления в OR
	// не обещан, и ''::inet справа уронил бы запрос отказом типа даже там,
	// где фильтра нет. NULL же сравнивается тихо и ни с чем не совпадает.
	return `
		SELECT e.id, e.user_id, coalesce(u.nickname, ''), e.login,
		       host(e.ip), e.subnet::text, e.user_agent, e.ok, e.created_at
		FROM login_events e LEFT JOIN users u ON u.id = e.user_id
		WHERE ($1::bigint IS NULL OR e.user_id = $1)
		  AND ($2 = '' OR e.ip = nullif($2, '')::inet)
		  AND (NOT $3 OR NOT e.ok)
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $4`
}

// Запрос вынесен функцией: «только удачные входы» — решение, которое
// снаружи не видно, а забыть его легко. Проверяет его тест.
func subnetsByUserSQL() string {
	return `SELECT user_id, count(DISTINCT subnet)
		FROM login_events
		WHERE ok AND user_id IS NOT NULL AND created_at >= $1
		GROUP BY user_id`
}

// SharedAccount — аккаунт, в который за срок входили из нескольких
// подсетей. Не обвинение, а повод посмотреть: см. SharedLoginSubnets.
type SharedAccount struct {
	UserID   int64
	Nickname string
	Subnets  int
	IPs      int
	LastAt   time.Time
}

// SharedAccounts — аккаунты с min и более подсетями за срок.
func (r *Logins) SharedAccounts(ctx context.Context, since time.Time, min int) ([]SharedAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.user_id, u.nickname,
		       count(DISTINCT e.subnet), count(DISTINCT e.ip), max(e.created_at)
		FROM login_events e JOIN users u ON u.id = e.user_id
		WHERE e.ok AND e.created_at >= $1
		GROUP BY e.user_id, u.nickname
		HAVING count(DISTINCT e.subnet) >= $2
		ORDER BY count(DISTINCT e.subnet) DESC, lower(u.nickname)`, since, min)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedAccount
	for rows.Next() {
		var a SharedAccount
		if err := rows.Scan(&a.UserID, &a.Nickname, &a.Subnets, &a.IPs, &a.LastAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SharedIP — адрес, с которого входили в несколько аккаунтов. Случай,
// обратный SharedAccount: не «доступ раздали», а «доступов несколько
// у одного человека».
type SharedIP struct {
	IP       string
	Accounts []string
	LastAt   time.Time
}

// SharedIPs — адреса, под которыми за срок удачно входили больше чем
// в один аккаунт.
func (r *Logins) SharedIPs(ctx context.Context, since time.Time) ([]SharedIP, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT host(e.ip), array_agg(DISTINCT u.nickname), max(e.created_at)
		FROM login_events e JOIN users u ON u.id = e.user_id
		WHERE e.ok AND e.created_at >= $1
		GROUP BY e.ip
		HAVING count(DISTINCT e.user_id) > 1
		ORDER BY max(e.created_at) DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedIP
	for rows.Next() {
		var s SharedIP
		if err := rows.Scan(&s.IP, &s.Accounts, &s.LastAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Forget убирает записи старше срока. Единственный способ что-то стереть
// из журнала входов, и работает он по часам, а не по чьему-то желанию.
func (r *Logins) Forget(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM login_events WHERE created_at < $1`, before)
	return tag.RowsAffected(), err
}
