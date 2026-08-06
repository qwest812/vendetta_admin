package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SupremacyGames помнит игры, о которых воркер уже сообщил, чтобы после
// перезапуска приложения не рассказывать про них заново.
type SupremacyGames struct{ pool *pgxpool.Pool }

func NewSupremacyGames(pool *pgxpool.Pool) *SupremacyGames {
	return &SupremacyGames{pool: pool}
}

// Seen отбирает из переданных ID те, о которых уже сообщали.
func (r *SupremacyGames) Seen(ctx context.Context, gameIDs []string) (map[string]bool, error) {
	seen := make(map[string]bool, len(gameIDs))
	if len(gameIDs) == 0 {
		return seen, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT game_id FROM supremacy_seen_games WHERE game_id = ANY($1)`, gameIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	return seen, rows.Err()
}

// Mark запоминает игру. Повторная отметка того же ID молча ничего не меняет:
// нам важно время первого сообщения, а не последнего.
func (r *SupremacyGames) Mark(ctx context.Context, gameID, title string, at time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_seen_games (game_id, title, reported_at) VALUES ($1, $2, $3)
		 ON CONFLICT (game_id) DO NOTHING`, gameID, title, at)
	return err
}

// Forget выметает записи старше указанного момента.
func (r *SupremacyGames) Forget(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM supremacy_seen_games WHERE reported_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
