package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MapChecks считает, сколько раз за день человек смотрел карту. Счёт в базе,
// а не в памяти: перезапуск админки не должен раздавать новые проверки.
type MapChecks struct{ pool *pgxpool.Pool }

func NewMapChecks(pool *pgxpool.Pool) *MapChecks { return &MapChecks{pool: pool} }

// Day — сутки, к которым отнести проверку. Считаем по часам приложения:
// «в день» должно кончаться там же, где кончается день у людей.
func Day(at time.Time) time.Time {
	y, m, d := at.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// Spend занимает одну проверку. Второй ответ — сколько осталось после неё.
// Занимает и считает одним запросом: двое соседних вкладок одного человека
// не должны потратить одну и ту же последнюю проверку дважды.
func (r *MapChecks) Spend(ctx context.Context, userID int64, limit int, day time.Time) (bool, int, error) {
	if limit <= 0 {
		return false, 0, nil
	}

	var used int
	err := r.pool.QueryRow(ctx,
		`INSERT INTO user_map_checks (user_id, day, used) VALUES ($1, $2, 1)
		 ON CONFLICT (user_id, day) DO UPDATE SET used = user_map_checks.used + 1
		  WHERE user_map_checks.used < $3
		 RETURNING used`, userID, day, limit).Scan(&used)
	// Строк нет — значит, условие не пустило: проверки на сегодня кончились.
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, limit - used, nil
}

// Record отмечает открытие карты, не сверяясь с лимитом. Нужен для рута:
// лимит на него не распространяется, но в счёт открытий он входить должен —
// иначе аналитика показывала бы всех, кроме самого активного.
func (r *MapChecks) Record(ctx context.Context, userID int64, day time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO user_map_checks (user_id, day, used) VALUES ($1, $2, 1)
		 ON CONFLICT (user_id, day) DO UPDATE SET used = user_map_checks.used + 1`,
		userID, day)
	return err
}

// Refund возвращает занятую проверку. Нужен там, где поход не состоялся:
// игра не ответила, и человек не увидел ничего — брать за это плату нечестно.
func (r *MapChecks) Refund(ctx context.Context, userID int64, day time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE user_map_checks SET used = used - 1
		  WHERE user_id = $1 AND day = $2 AND used > 0`, userID, day)
	return err
}

// Left — сколько проверок осталось на сегодня. Ничего не занимает: страница
// показывает остаток и там, где карту не просят.
func (r *MapChecks) Left(ctx context.Context, userID int64, limit int, day time.Time) (int, error) {
	if limit <= 0 {
		return 0, nil
	}

	var used int
	err := r.pool.QueryRow(ctx,
		`SELECT used FROM user_map_checks WHERE user_id = $1 AND day = $2`, userID, day).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return limit, nil
	}
	if err != nil {
		return 0, err
	}
	if used >= limit {
		return 0, nil
	}
	return limit - used, nil
}

// Forget убирает счёт за прошедшие дни: он больше никому не нужен, а таблице
// незачем помнить каждый день каждого.
func (r *MapChecks) Forget(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM user_map_checks WHERE day < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MapDay — сколько карт открыли за сутки и кто именно.
type MapDay struct {
	Day   time.Time
	Total int
	By    []MapUser
}

// MapUser — один человек в дне. Ник хранится копией у пользователя, поэтому
// удалённый из админки не уносит с собой историю: строки остаются, а имя
// приходит пустым.
type MapUser struct {
	UserID   int64
	Nickname string
	Count    int
}

// Daily — открытия карт по дням, начиная с since и свежими вперёд. Считается
// по тому же счётчику, что и суточный лимит: несостоявшийся заход из него
// вычитается, поэтому здесь ровно те открытия, которые человек увидел.
func (r *MapChecks) Daily(ctx context.Context, since time.Time) ([]MapDay, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.day, c.user_id, coalesce(u.nickname, ''), c.used
		   FROM user_map_checks c LEFT JOIN users u ON u.id = c.user_id
		  WHERE c.day >= $1 AND c.used > 0
		  ORDER BY c.day DESC, c.used DESC, u.nickname`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MapDay
	for rows.Next() {
		var day time.Time
		var u MapUser
		if err := rows.Scan(&day, &u.UserID, &u.Nickname, &u.Count); err != nil {
			return nil, err
		}
		// Строки идут по дням подряд, поэтому день закрывается сам собой,
		// как только начался следующий.
		if n := len(out); n == 0 || !out[n-1].Day.Equal(day) {
			out = append(out, MapDay{Day: day})
		}
		d := &out[len(out)-1]
		d.Total += u.Count
		d.By = append(d.By, u)
	}
	return out, rows.Err()
}
