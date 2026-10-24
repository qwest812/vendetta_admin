package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// Provinces — провинции партий по заходам сбора коалиций: последние
// владельцы, счёт по дням и переходы между игроками. Сырьё для поиска
// мультиаккаунтов; пишется попутно, в игру ради него никто не ходит.
type Provinces struct{ pool *pgxpool.Pool }

func NewProvinces(pool *pgxpool.Pool) *Provinces { return &Provinces{pool: pool} }

// PrevOwners — владельцы провинций с прошлого захода. Пусто — заходим
// впервые, и сравнивать не с чем.
func (r *Provinces) PrevOwners(ctx context.Context, gameID string) (map[int]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT province_id, site_user_id FROM supremacy_province_owners WHERE game_id = $1`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var id int
		var who string
		if err := rows.Scan(&id, &who); err != nil {
			return nil, err
		}
		out[id] = who
	}
	return out, rows.Err()
}

// SaveProvinces пишет заход одной транзакцией: владельцев взамен прежних
// (кончилась партия — удаляет их, сравнивать больше не с чем), счёт на
// день и переходы — прибавкой к уже накопленным за партию.
func (r *Provinces) SaveProvinces(ctx context.Context, snap domain.ProvinceSnapshot, at time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM supremacy_province_owners WHERE game_id = $1`, snap.GameID); err != nil {
		return err
	}
	// Партия целиком — карта, день, коалиции: по ним и владельцам рисуется
	// мини-карта «Ваших партий». Кончилась — убираем вместе с владельцами.
	if snap.Done || snap.MapID == "" {
		if _, err := tx.Exec(ctx, `DELETE FROM supremacy_province_games WHERE game_id = $1`, snap.GameID); err != nil {
			return err
		}
	} else {
		teams, err := json.Marshal(snap.Teams)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_province_games (game_id, map_id, day, teams, taken_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (game_id) DO UPDATE SET
			     map_id = EXCLUDED.map_id, day = EXCLUDED.day, teams = EXCLUDED.teams, taken_at = EXCLUDED.taken_at`,
			snap.GameID, snap.MapID, snap.Day, teams, at); err != nil {
			return err
		}
	}
	if !snap.Done && len(snap.Owners) > 0 {
		ids, who := make([]int32, 0, len(snap.Owners)), make([]string, 0, len(snap.Owners))
		for id, w := range snap.Owners {
			ids, who = append(ids, int32(id)), append(who, w)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_province_owners (game_id, province_id, site_user_id)
			 SELECT $1, u.id, u.who FROM unnest($2::int[], $3::text[]) AS u(id, who)`,
			snap.GameID, ids, who); err != nil {
			return err
		}
	}

	if len(snap.Counts) > 0 {
		users, counts := make([]string, 0, len(snap.Counts)), make([]int32, 0, len(snap.Counts))
		for u, n := range snap.Counts {
			users, counts = append(users, u), append(counts, int32(n))
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_province_counts (game_id, day, site_user_id, provinces, taken_at)
			 SELECT $1, $2, u.who, u.n, $5 FROM unnest($3::text[], $4::int[]) AS u(who, n)
			 ON CONFLICT (game_id, day, site_user_id) DO UPDATE SET
			     provinces = EXCLUDED.provinces, taken_at = EXCLUDED.taken_at`,
			snap.GameID, snap.Day, users, counts, at); err != nil {
			return err
		}
	}

	for _, t := range snap.Transfers {
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_province_transfers
			     (game_id, from_site, to_site, same_team, provinces, first_day, last_day)
			 VALUES ($1, $2, $3, $4, $5, $6, $6)
			 ON CONFLICT (game_id, from_site, to_site, same_team) DO UPDATE SET
			     provinces = supremacy_province_transfers.provinces + EXCLUDED.provinces,
			     last_day = EXCLUDED.last_day`,
			snap.GameID, t.From, t.To, t.SameTeam, t.Provinces, snap.Day); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ScannedGame — партия игрока по данным обхода: где у него есть провинции
// на последнем заходе.
type ScannedGame struct {
	GameID  string
	Title   string
	MapID   string
	Day     int
	Mine    int
	Total   int
	Teams   map[string]int
	TakenAt time.Time
}

// ScannedGames — идущие партии, где у игрока есть провинции по последнему
// заходу обхода, свежие сверху.
func (r *Provinces) ScannedGames(ctx context.Context, siteUserID string, limit int) ([]ScannedGame, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT g.game_id, coalesce(w.title, ''), g.map_id, g.day, g.teams, g.taken_at,
		        count(*) FILTER (WHERE o.site_user_id = $1),
		        count(*)
		   FROM supremacy_province_games g
		   JOIN supremacy_province_owners o ON o.game_id = g.game_id
		   LEFT JOIN supremacy_watched_games w ON w.game_id = g.game_id
		  WHERE g.game_id IN (SELECT game_id FROM supremacy_province_owners WHERE site_user_id = $1)
		  GROUP BY g.game_id, w.title
		  ORDER BY g.taken_at DESC
		  LIMIT $2`, siteUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScannedGame
	for rows.Next() {
		var g ScannedGame
		var teams []byte
		if err := rows.Scan(&g.GameID, &g.Title, &g.MapID, &g.Day, &teams, &g.TakenAt, &g.Mine, &g.Total); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(teams, &g.Teams); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ScannedOwners — владельцы провинций партии по последнему заходу обхода
// (номер провинции → игрок на сайте) и сама партия. Отдаётся, только если
// у siteUserID в ней есть провинции: чужие партии так не посмотреть.
func (r *Provinces) ScannedOwners(ctx context.Context, gameID, siteUserID string) (map[int]string, ScannedGame, bool, error) {
	var g ScannedGame
	var teams []byte
	err := r.pool.QueryRow(ctx,
		`SELECT g.game_id, g.map_id, g.day, g.teams, g.taken_at
		   FROM supremacy_province_games g
		  WHERE g.game_id = $1
		    AND EXISTS (SELECT 1 FROM supremacy_province_owners o WHERE o.game_id = g.game_id AND o.site_user_id = $2)`,
		gameID, siteUserID).Scan(&g.GameID, &g.MapID, &g.Day, &teams, &g.TakenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, g, false, nil
	}
	if err != nil {
		return nil, g, false, err
	}
	if err := json.Unmarshal(teams, &g.Teams); err != nil {
		return nil, g, false, err
	}
	owners, err := r.PrevOwners(ctx, gameID)
	return owners, g, err == nil, err
}
