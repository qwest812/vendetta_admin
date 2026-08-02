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

func (r *Sessions) Create(ctx context.Context, tokenHash []byte, userID int64, csrf string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, csrf, expiresAt)
	return err
}

// Lookup возвращает живую сессию вместе с пользователем.
// Заблокированные пользователи сессию не получают.
func (r *Sessions) Lookup(ctx context.Context, tokenHash []byte) (*Session, error) {
	var s Session
	var u domain.User
	err := r.pool.QueryRow(ctx,
		`SELECT s.csrf_token, s.expires_at, u.id, u.email, u.password_hash, u.role, u.is_active, u.created_by, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.expires_at > now() AND u.is_active`, tokenHash).
		Scan(&s.CSRFToken, &s.ExpiresAt,
			&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedBy, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
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

func (r *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return tag.RowsAffected(), err
}
