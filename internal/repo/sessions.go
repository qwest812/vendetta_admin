package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Sessions struct{ pool *pgxpool.Pool }

func NewSessions(pool *pgxpool.Pool) *Sessions { return &Sessions{pool: pool} }

type Session struct {
	User      *domain.User
	CSRFToken string
	ExpiresAt time.Time
}

// SessionKind — род сессии: кука сайта или токен расширения. Один род
// другим не подменяется, см. Lookup.
type SessionKind string

const (
	SessionBrowser   SessionKind = "browser"
	SessionExtension SessionKind = "extension"
)

// Create заводит сессию. ip — адрес, с которого вошли; пустая строка
// означает «адрес не разобрали», и в базе останется NULL: врать нулевым
// адресом хуже, чем признать, что места мы не знаем.
func (r *Sessions) Create(ctx context.Context, tokenHash []byte, userID int64, csrf, ip string,
	expiresAt time.Time, kind SessionKind) error {

	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, csrf_token, ip, expires_at, kind)
		 VALUES ($1, $2, $3, nullif($4, '')::inet, $5, $6)`,
		tokenHash, userID, csrf, ip, expiresAt, string(kind))
	return err
}

// LiveSession — человек и адрес, с которого он сейчас в системе. Говорит
// не «когда входили», а «откуда сидят»: два адреса у одного человека
// в один момент — самое внятное, что вообще можно сказать про раздачу
// доступа.
type LiveSession struct {
	UserID   int64
	Nickname string
	// IP пуст у сессий, заведённых до того, как адрес стали запоминать,
	// и у тех, чей адрес не разобрался.
	IP string
	// Sessions — сколько живых сессий с этого адреса. Обычно больше одной:
	// каждый вход в новом браузере заводит свою, а живут они неделю.
	Sessions int
	LastAt   time.Time
}

// Live — кто сейчас в системе, по строке на «человек + адрес».
//
// Не по строке на сессию: за неделю их набегает по десятку на человека —
// с каждого перезапуска и каждого браузера, — и блок превращался бы
// в стену одинаковых строк, в которой второй адрес и не заметишь. А второй
// адрес — ровно то, ради чего блок есть.
//
// Уборщик протухшие сессии удаляет раз в час, поэтому условие по сроку
// здесь своё: страница не должна показывать вчерашнее как живое.
func (r *Sessions) Live(ctx context.Context) ([]LiveSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.user_id, u.nickname, coalesce(host(s.ip), ''),
		       count(*), max(s.created_at)
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.expires_at > now()
		GROUP BY s.user_id, u.nickname, s.ip
		ORDER BY lower(u.nickname), max(s.created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveSession
	for rows.Next() {
		var s LiveSession
		if err := rows.Scan(&s.UserID, &s.Nickname, &s.IP, &s.Sessions, &s.LastAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Lookup возвращает живую сессию вместе с пользователем. Пользователь
// читается на каждый запрос, поэтому снятый доступ и правки профиля
// применяются сразу, без перелогина.
// Заблокированные пользователи сессию не получают.
//
// kind — какого рода сессию ищем. Токен расширения не годится как кука
// сайта, а кука — как токен: у каждой дороги своя защита, и подмена обходила
// бы чужую.
func (r *Sessions) Lookup(ctx context.Context, tokenHash []byte, kind SessionKind) (*Session, error) {
	var s Session
	var u domain.User
	var email *string // почта необязательна, в базе может быть NULL
	// Колонки и приёмники берём общие с остальными запросами о пользователе:
	// свой список здесь однажды уже отстал от общего, и новое поле молча
	// не доезжало до сессии — то есть до каждой страницы.
	err := r.pool.QueryRow(ctx,
		`SELECT s.csrf_token, s.expires_at, `+userColumnsOf("u")+`
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.kind = $2 AND s.expires_at > now() AND u.is_active`,
		tokenHash, string(kind)).
		Scan(append([]any{&s.CSRFToken, &s.ExpiresAt}, userScanTargets(&u, &email)...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if email != nil {
		u.Email = *email
	}
	s.User = &u
	return &s, nil
}

func (r *Sessions) Touch(ctx context.Context, tokenHash []byte, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE token_hash = $1`, tokenHash, expiresAt)
	return err
}

func (r *Sessions) Delete(ctx context.Context, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteByUser разлогинивает пользователя во всех браузерах: вызывается
// при понижении роли, блокировке и смене пароля.
func (r *Sessions) DeleteByUser(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// CountByUser — сколько живых сессий этого рода у человека. Профилю этого
// хватает, чтобы сказать «расширение подключено» и предложить отключить.
func (r *Sessions) CountByUser(ctx context.Context, userID int64, kind SessionKind) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND kind = $2 AND expires_at > now()`,
		userID, string(kind)).Scan(&n)
	return n, err
}

// DeleteByUserKind обрывает сессии одного рода: «отключить расширение»
// не должно выкидывать человека из браузера, в котором он это нажал.
func (r *Sessions) DeleteByUserKind(ctx context.Context, userID int64, kind SessionKind) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND kind = $2`, userID, string(kind))
	return err
}

func (r *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return tag.RowsAffected(), err
}
