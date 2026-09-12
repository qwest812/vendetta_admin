package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Users struct{ pool *pgxpool.Pool }

func NewUsers(pool *pgxpool.Pool) *Users { return &Users{pool: pool} }

const userColumns = `id, email, nickname, password_hash, role, is_active, games_access, plan, map_checks, created_by, created_at, full_name, city, game_id`

// userColumnsOf — те же колонки с именем таблицы впереди. Сессия читает
// пользователя join-ом, и набор у неё должен быть ровно тот же: разошедшиеся
// списки сборку не ломают, поле просто молча остаётся пустым — и замечают
// это уже на странице, если вообще замечают.
func userColumnsOf(alias string) string {
	cols := strings.Split(userColumns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// userScanTargets — куда складывать userColumns, в том же порядке. Список
// один на оба запроса по той же причине, что и колонки: разойтись порядку
// негде, когда он написан один раз.
//
// Почта необязательна и хранится как NULL, поэтому едет через отдельный
// указатель, а в домен попадает пустой строкой.
func userScanTargets(u *domain.User, email **string) []any {
	return []any{&u.ID, email, &u.Nickname, &u.PasswordHash, &u.Role, &u.IsActive,
		&u.GamesAccess, &u.Plan, &u.MapChecks, &u.CreatedBy, &u.CreatedAt,
		&u.FullName, &u.City, &u.GameID}
}

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var email *string
	err := row.Scan(userScanTargets(&u, &email)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if email != nil {
		u.Email = *email
	}
	return &u, nil
}

func (r *Users) ByID(ctx context.Context, id int64) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// ByLogin ищет пользователя по тому, что он ввёл при входе: это либо почта,
// либо ник. Оба поля уникальны без учёта регистра, поэтому строка совпадёт
// максимум с одной записью.
func (r *Users) ByLogin(ctx context.Context, login string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE lower(email) = lower($1) OR lower(nickname) = lower($1)`, login))
}

func (r *Users) List(ctx context.Context) ([]*domain.User, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users
		 ORDER BY CASE role WHEN 'root' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, lower(nickname)`)
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

// UpdateProfile сохраняет то, что человек написал о себе сам. Роли, ника
// и пароля это не касается — их меняют в «Доступах».
func (r *Users) UpdateProfile(ctx context.Context, id int64, fullName, city, gameID string) error {
	return r.exec(ctx,
		`UPDATE users SET full_name = $2, city = $3, game_id = $4 WHERE id = $1`,
		id, fullName, city, gameID)
}

// Create заводит пользователя. Пустая почта допустима — она уходит в NULL,
// чтобы безадресные пользователи не конфликтовали друг с другом по индексу.
func (r *Users) Create(ctx context.Context, email, nickname, passwordHash string, role domain.Role, createdBy *int64) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`INSERT INTO users (email, nickname, password_hash, role, created_by)
		 VALUES (NULLIF($1, ''), $2, $3, $4, $5) RETURNING `+userColumns,
		email, nickname, passwordHash, role, createdBy))
	if isUniqueViolation(err, "users_email_key") {
		return nil, domain.ErrEmailTaken
	}
	if isUniqueViolation(err, "users_nickname_key") {
		return nil, domain.ErrNickTaken
	}
	return u, err
}

func (r *Users) SetRole(ctx context.Context, id int64, role domain.Role) error {
	return r.exec(ctx, `UPDATE users SET role = $2 WHERE id = $1 AND role <> 'root'`, id, role)
}

// SetGamesAccess выдаёт и снимает доступ к разделу «Игры». Рута не трогаем:
// он проходит по роли, и флаг у него ничего не решает.
func (r *Users) SetGamesAccess(ctx context.Context, id int64, allowed bool) error {
	return r.exec(ctx, `UPDATE users SET games_access = $2 WHERE id = $1 AND role <> 'root'`, id, allowed)
}

// SetPlan меняет пакет доступа — в том числе рутовый, и в том числе свой
// собственный. Исключения для рута здесь нет, в отличие от роли, блокировки
// и удаления: теми рут закрыл бы вход сам себе, и вернуть его было бы
// некому, — а пакет он в любой момент меняет обратно.
//
// Ради этого всё и затевалось: пакет действует и на рута, и сменить его
// себе — единственный способ увидеть админку глазами обычного человека.
func (r *Users) SetPlan(ctx context.Context, id int64, plan domain.Plan) error {
	return r.exec(ctx, `UPDATE users SET plan = $2 WHERE id = $1`, id, plan)
}

// SetMapChecks меняет дневное число проверок карты. Рута не трогаем, как
// и в остальных правах: он вне счёта.
func (r *Users) SetMapChecks(ctx context.Context, id int64, checks int) error {
	return r.exec(ctx, `UPDATE users SET map_checks = $2 WHERE id = $1 AND role <> 'root'`, id, checks)
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
func (r *Users) EnsureRoot(ctx context.Context, email, nickname, passwordHash string) (bool, error) {
	// Пакет рута — ультра с самого начала. В проверках он и так вне счёта,
	// но пакет у него виден в «Доступах», и «базовый» у того, кому открыто
	// всё, читался бы как недосмотр.
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO users (email, nickname, password_hash, role, plan)
		 VALUES (NULLIF($1, ''), $2, $3, 'root', 'ultra')
		 ON CONFLICT DO NOTHING`, email, nickname, passwordHash)
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
