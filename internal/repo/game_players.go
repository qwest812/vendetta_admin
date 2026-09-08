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
//
// Таблиц у раздела две: в supremacy_players лежит сегодняшнее состояние,
// в supremacy_ban_events — история банов, по строке на замеченную смену.
type GamePlayers struct{ pool *pgxpool.Pool }

func NewGamePlayers(pool *pgxpool.Pool) *GamePlayers { return &GamePlayers{pool: pool} }

// Save записывает состав партии. Дата бана ставится в момент, когда бан
// увидели впервые, и держится, пока он есть; снятие бана обнуляет её —
// дата без бана означала бы «сидит до сих пор». Заодно тем же запросом
// пишется журнал: смену статуса иначе никто бы не запомнил.
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
		`WITH incoming AS (
		     SELECT * FROM unnest($1::text[], $2::text[], $3::boolean[])
		            AS u(site_user_id, nickname, banned)
		 ),
		 -- Прошлый статус читаем до записи, и порядок веток тут ни при чём:
		 -- все они видят базу такой, какой она была на начало запроса.
		 prev AS (
		     SELECT p.site_user_id, p.banned
		       FROM supremacy_players p JOIN incoming i USING (site_user_id)
		 ),
		 saved AS (
		     INSERT INTO supremacy_players
		         (site_user_id, nickname, banned, banned_at, seen_at, seen_game_id)
		     SELECT i.site_user_id, i.nickname, i.banned,
		            CASE WHEN i.banned THEN $4::timestamptz END, $4, $5
		       FROM incoming i
		     ON CONFLICT (site_user_id) DO UPDATE SET
		         nickname = EXCLUDED.nickname,
		         banned = EXCLUDED.banned,
		         banned_at = CASE
		             WHEN NOT EXCLUDED.banned THEN NULL
		             WHEN supremacy_players.banned THEN supremacy_players.banned_at
		             ELSE EXCLUDED.seen_at
		         END,
		         seen_at = EXCLUDED.seen_at,
		         seen_game_id = EXCLUDED.seen_game_id
		 )
		 -- В журнал попадает только смена статуса, а не каждый заход:
		 -- иначе он вырос бы на сорок строк с каждой открытой картой.
		 -- Незнакомец без бана события не рождает, с баном — рождает:
		 -- для нас он им и начался.
		 INSERT INTO supremacy_ban_events (site_user_id, banned, noticed_at, game_id)
		 SELECT i.site_user_id, i.banned, $4, $5
		   FROM incoming i LEFT JOIN prev USING (site_user_id)
		  WHERE i.banned IS DISTINCT FROM COALESCE(prev.banned, false)`,
		users, nicks, banned, at, gameID)
	return err
}

// BanHistory отдаёт журнал банов игрока, свежие события первыми. Пусто —
// обычное дело: у большинства статус не менялся ни разу.
func (r *GamePlayers) BanHistory(ctx context.Context, siteUserID string) ([]domain.BanEvent, error) {
	if siteUserID == "" {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT site_user_id, banned, noticed_at, game_id
		   FROM supremacy_ban_events WHERE site_user_id = $1
		  ORDER BY noticed_at DESC, id DESC`, siteUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.BanEvent
	for rows.Next() {
		var e domain.BanEvent
		if err := rows.Scan(&e.SiteUserID, &e.Banned, &e.NoticedAt, &e.GameID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
