package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// CheckedGames — личные списки проверенных партий: у каждого свой.
type CheckedGames struct{ pool *pgxpool.Pool }

func NewCheckedGames(pool *pgxpool.Pool) *CheckedGames { return &CheckedGames{pool: pool} }

// Mark отмечает, что пользователь смотрел партию. Партия, уже бывшая
// в списке, не заводит вторую строку — у неё двигается дата.
func (r *CheckedGames) Mark(ctx context.Context, userID int64, gameID, title string, at time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO user_checked_games (user_id, game_id, title, checked_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, game_id) DO UPDATE
		    SET checked_at = EXCLUDED.checked_at,
		        -- Название могло измениться, но пустым его не затираем:
		        -- молчание сайта не повод терять уже известное.
		        title = CASE WHEN EXCLUDED.title = '' THEN user_checked_games.title
		                     ELSE EXCLUDED.title END`,
		userID, gameID, title, at)
	return err
}

// List — партии, которые пользователь смотрел не раньше since; свежие сверху.
func (r *CheckedGames) List(ctx context.Context, userID int64, since time.Time) ([]domain.CheckedGame, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT game_id, title, checked_at
		   FROM user_checked_games
		  WHERE user_id = $1 AND checked_at >= $2
		  ORDER BY checked_at DESC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.CheckedGame
	for rows.Next() {
		var g domain.CheckedGame
		if err := rows.Scan(&g.GameID, &g.Title, &g.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Forget убирает записи старше olderThan. Список и без уборки показывает
// только свежее — она нужна затем, чтобы таблица не росла вечно.
func (r *CheckedGames) Forget(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM user_checked_games WHERE checked_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
