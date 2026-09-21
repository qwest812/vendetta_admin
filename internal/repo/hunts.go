package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// Hunts — поиск игроков в лобби: кого ищем и где уже нашли.
type Hunts struct{ pool *pgxpool.Pool }

func NewHunts(pool *pgxpool.Pool) *Hunts { return &Hunts{pool: pool} }

// Targets — кого ищем, по нику.
func (r *Hunts) Targets(ctx context.Context) ([]domain.HuntTarget, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, site_user_id, nickname, added_at
		   FROM supremacy_hunt_targets ORDER BY lower(nickname), site_user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.HuntTarget
	for rows.Next() {
		var t domain.HuntTarget
		if err := rows.Scan(&t.ID, &t.SiteUserID, &t.Nickname, &t.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Add начинает искать игрока. Уже искомого не дублирует — только
// освежает ник, если его передали.
func (r *Hunts) Add(ctx context.Context, siteUserID, nickname string, by int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_hunt_targets (site_user_id, nickname, added_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (site_user_id) DO UPDATE
		    SET nickname = CASE WHEN EXCLUDED.nickname <> '' THEN EXCLUDED.nickname
		                        ELSE supremacy_hunt_targets.nickname END`,
		siteUserID, nickname, by)
	return err
}

// Remove перестаёт искать. Находки уходят вместе с ним.
func (r *Hunts) Remove(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM supremacy_hunt_targets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Find — ищем ли игрока с этим номером на сайте. Ноль — не ищем.
func (r *Hunts) Find(ctx context.Context, siteUserID string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM supremacy_hunt_targets WHERE site_user_id = $1`, siteUserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// Seen — сообщали ли уже, что игрок в этой партии.
func (r *Hunts) Seen(ctx context.Context, targetID int64, gameID string) (bool, error) {
	var seen bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supremacy_hunt_hits WHERE target_id = $1 AND game_id = $2)`,
		targetID, gameID).Scan(&seen)
	return seen, err
}

// Record запоминает находку — после того, как о ней сообщили. Заодно
// освежает ник искомого: в лобби он всегда нынешний.
func (r *Hunts) Record(ctx context.Context, h domain.HuntHit) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_hunt_hits (target_id, game_id, title, nickname, nation, found_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (target_id, game_id) DO NOTHING`,
		h.TargetID, h.GameID, h.Title, h.Nickname, h.Nation, h.FoundAt); err != nil {
		return err
	}
	if h.Nickname != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE supremacy_hunt_targets SET nickname = $2 WHERE id = $1`,
			h.TargetID, h.Nickname); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Hits — последние находки, свежие сверху.
func (r *Hunts) Hits(ctx context.Context, limit int) ([]domain.HuntHit, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT h.target_id, t.site_user_id, h.nickname, h.game_id, h.title, h.nation, h.found_at
		   FROM supremacy_hunt_hits h
		   JOIN supremacy_hunt_targets t ON t.id = h.target_id
		  ORDER BY h.found_at DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.HuntHit
	for rows.Next() {
		var h domain.HuntHit
		if err := rows.Scan(&h.TargetID, &h.SiteUserID, &h.Nickname, &h.GameID,
			&h.Title, &h.Nation, &h.FoundAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
