package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// HeroPlans хранит отложенные размещения героев.
type HeroPlans struct{ pool *pgxpool.Pool }

func NewHeroPlans(pool *pgxpool.Pool) *HeroPlans { return &HeroPlans{pool: pool} }

const heroPlanColumns = `id, game_id, unit, hero, province, city, day, next_at, run_at, result, attempts, added_at`

func scanHeroPlans(rows pgx.Rows) ([]domain.HeroPlan, error) {
	defer rows.Close()
	var out []domain.HeroPlan
	for rows.Next() {
		var (
			p           domain.HeroPlan
			next, runAt *time.Time
		)
		if err := rows.Scan(&p.ID, &p.GameID, &p.Unit, &p.Hero, &p.Province, &p.City, &p.Day,
			&next, &runAt, &p.Result, &p.Attempts, &p.AddedAt); err != nil {
			return nil, err
		}
		if next != nil {
			p.NextAt = *next
		}
		if runAt != nil {
			p.RunAt = *runAt
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Add заводит план. Срок — первый заход воркера: он сам узнает в партии,
// наступил ли день, и если нет — назначит заход на его начало.
func (r *HeroPlans) Add(ctx context.Context, p domain.HeroPlan, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_hero_plans (game_id, unit, hero, province, city, day, next_at, added_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.GameID, p.Unit, p.Hero, p.Province, p.City, p.Day, p.NextAt, actorID)
	return err
}

// List — планы партии, свежие сверху.
func (r *HeroPlans) List(ctx context.Context, gameID string) ([]domain.HeroPlan, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+heroPlanColumns+` FROM supremacy_hero_plans WHERE game_id = $1 ORDER BY id DESC`, gameID)
	if err != nil {
		return nil, err
	}
	return scanHeroPlans(rows)
}

// Due — открытые планы, у которых подошёл срок.
func (r *HeroPlans) Due(ctx context.Context, now time.Time) ([]domain.HeroPlan, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+heroPlanColumns+` FROM supremacy_hero_plans
		  WHERE next_at IS NOT NULL AND next_at <= $1 ORDER BY next_at`, now)
	if err != nil {
		return nil, err
	}
	return scanHeroPlans(rows)
}

// Mark записывает итог захода и следующий срок; нулевой срок план закрывает.
func (r *HeroPlans) Mark(ctx context.Context, id int64, at time.Time, result string, next time.Time) error {
	var nextAt *time.Time
	if !next.IsZero() {
		nextAt = &next
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_hero_plans
		    SET run_at = $2, result = $3, next_at = $4, attempts = attempts + 1
		  WHERE id = $1`,
		id, at, result, nextAt)
	return err
}

// Remove убирает план партии — открытый или закрытый.
func (r *HeroPlans) Remove(ctx context.Context, gameID string, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM supremacy_hero_plans WHERE id = $1 AND game_id = $2`, id, gameID)
	return err
}
