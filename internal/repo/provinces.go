package repo

import (
	"context"
	"time"

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
