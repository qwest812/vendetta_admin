package repo

import (
	"context"
	"time"

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
//
// Тип у $4 приходится называть вслух: внутри CASE без ELSE выводить его
// Postgres не из чего, он берёт text — и упирается в timestamptz колонки.
func (r *GamePlayers) Save(ctx context.Context, list []domain.GamePlayer) error {
	if len(list) == 0 {
		return nil
	}

	// Одним запросом, а не строкой за строкой: состав партии — это четыре
	// десятка человек, и сорок обращений к базе ради них излишни. Повторы
	// убираем заранее — ON CONFLICT DO UPDATE не правит одну строку дважды
	// в одном запросе.
	users, nicks, banned := make([]string, 0, len(list)), make([]string, 0, len(list)), make([]bool, 0, len(list))
	seen := make(map[string]int, len(list))
	var at time.Time
	var gameID string
	for _, p := range list {
		if p.SiteUserID == "" {
			continue
		}
		at, gameID = p.SeenAt, p.SeenGameID
		if i, ok := seen[p.SiteUserID]; ok {
			nicks[i], banned[i] = p.Nickname, p.Banned
			continue
		}
		seen[p.SiteUserID] = len(users)
		users = append(users, p.SiteUserID)
		nicks = append(nicks, p.Nickname)
		banned = append(banned, p.Banned)
	}
	if len(users) == 0 {
		return nil
	}

	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_players
		     (site_user_id, nickname, banned, banned_at, seen_at, seen_game_id)
		 SELECT u.site_user_id, u.nickname, u.banned,
		        CASE WHEN u.banned THEN $4::timestamptz END, $4, $5
		   FROM unnest($1::text[], $2::text[], $3::boolean[])
		        AS u(site_user_id, nickname, banned)
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
		users, nicks, banned, at, gameID)
	return err
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
