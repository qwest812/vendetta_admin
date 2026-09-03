package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// GamePlayers — то, что игра рассказала про игроков при заходе в партию.
// Пишется целиком на каждый заход: состав всё равно перед глазами, а знать
// о бане полезно и потом, когда человек больше не попадётся.
type GamePlayers struct{ pool *pgxpool.Pool }

func NewGamePlayers(pool *pgxpool.Pool) *GamePlayers { return &GamePlayers{pool: pool} }

// Save записывает состав партии. Дата бана ставится в момент, когда бан
// увидели впервые, и держится, пока он есть; снятие бана обнуляет её —
// дата без бана означала бы «сидит до сих пор».
func (r *GamePlayers) Save(ctx context.Context, list []domain.GamePlayer) error {
	if len(list) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, p := range list {
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_players
			     (site_user_id, nickname, banned, banned_at, seen_at, seen_game_id)
			 VALUES ($1, $2, $3, CASE WHEN $3 THEN $4 END, $4, $5)
			 ON CONFLICT (site_user_id) DO UPDATE SET
			     nickname = EXCLUDED.nickname,
			     banned = EXCLUDED.banned,
			     banned_at = CASE
			         WHEN NOT EXCLUDED.banned THEN NULL
			         WHEN supremacy_players.banned THEN supremacy_players.banned_at
			         ELSE EXCLUDED.seen_at
			     END,
			     seen_at = EXCLUDED.seen_at,
			     seen_game_id = EXCLUDED.seen_game_id`,
			p.SiteUserID, p.Nickname, p.Banned, p.SeenAt, p.SeenGameID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ByGameIDs отдаёт известное про игроков по их номерам на сайте игры —
// тем же, что стоят в карточках как «игровой ID».
func (r *GamePlayers) ByGameIDs(ctx context.Context, siteUserIDs []string) (map[string]domain.GamePlayer, error) {
	found := make(map[string]domain.GamePlayer, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return found, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT site_user_id, nickname, banned, banned_at, seen_at, seen_game_id
		   FROM supremacy_players WHERE site_user_id = ANY($1)`, siteUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var p domain.GamePlayer
		if err := rows.Scan(&p.SiteUserID, &p.Nickname, &p.Banned,
			&p.BannedAt, &p.SeenAt, &p.SeenGameID); err != nil {
			return nil, err
		}
		found[p.SiteUserID] = p
	}
	return found, rows.Err()
}
