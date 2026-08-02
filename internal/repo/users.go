package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Users struct{ pool *pgxpool.Pool }

func NewUsers(pool *pgxpool.Pool) *Users { return &Users{pool: pool} }

const userColumns = `id, email, password_hash, role, is_active, created_by, created_at`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedBy, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Users) ByID(ctx context.Context, id int64) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

func (r *Users) ByEmail(ctx context.Context, email string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, email))
}

func (r *Users) List(ctx context.Context) ([]*domain.User, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users
		 ORDER BY CASE role WHEN 'root' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *Users) Create(ctx context.Context, email, passwordHash string, role domain.Role, createdBy *int64) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, created_by)
		 VALUES ($1, $2, $3, $4) RETURNING `+userColumns,
		email, passwordHash, role, createdBy))
	if isUniqueViolation(err, "users_email_key") {
		return nil, domain.ErrEmailTaken
	}
	return u, err
}

func (r *Users) SetRole(ctx context.Context, id int64, role domain.Role) error {
	return r.exec(ctx, `UPDATE users SET role = $2 WHERE id = $1 AND role <> 'root'`, id, role)
}

func (r *Users) SetActive(ctx context.Context, id int64, active bool) error {
	return r.exec(ctx, `UPDATE users SET is_active = $2 WHERE id = $1 AND role <> 'root'`, id, active)
}

func (r *Users) SetPassword(ctx context.Context, id int64, passwordHash string) error {
	return r.exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
}

func (r *Users) Delete(ctx context.Context, id int64) error {
	return r.exec(ctx, `DELETE FROM users WHERE id = $1 AND role <> 'root'`, id)
}

// EnsureRoot создаёт рута при первом запуске. Возвращает true, если рут был создан.
func (r *Users) EnsureRoot(ctx context.Context, email, passwordHash string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, role)
		 VALUES ($1, $2, 'root')
		 ON CONFLICT DO NOTHING`, email, passwordHash)
	if err != nil {
		return false, fmt.Errorf("создание рута: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Users) exec(ctx context.Context, sql string, args ...any) error {
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
