package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// GameTasks хранит, что админка делает в партиях Supremacy сама.
type GameTasks struct{ pool *pgxpool.Pool }

func NewGameTasks(pool *pgxpool.Pool) *GameTasks { return &GameTasks{pool: pool} }

// Get возвращает настройки партии. Партии, о которой ещё не спрашивали,
// в базе нет — это не ошибка, а «ничего не включено».
func (r *GameTasks) Get(ctx context.Context, gameID string) (*domain.GameTask, error) {
	var (
		t         domain.GameTask
		lastRun   *time.Time
		updatedBy *int64
	)
	err := r.pool.QueryRow(ctx,
		`SELECT game_id, title, hero_deploy, updated_by, updated_at, last_run_at, last_result
		   FROM supremacy_game_tasks WHERE game_id = $1`, gameID).
		Scan(&t.GameID, &t.Title, &t.HeroDeploy, &updatedBy, &t.UpdatedAt, &lastRun, &t.LastResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return &domain.GameTask{GameID: gameID}, nil
	}
	if err != nil {
		return nil, err
	}
	t.UpdatedBy = updatedBy
	if lastRun != nil {
		t.LastRunAt = *lastRun
	}
	return &t, nil
}

// SetHeroDeploy включает или выключает автопризыв пехоты в партии.
func (r *GameTasks) SetHeroDeploy(ctx context.Context, gameID, title string, on bool, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_game_tasks (game_id, title, hero_deploy, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (game_id) DO UPDATE
		    SET hero_deploy = EXCLUDED.hero_deploy,
		        updated_by  = EXCLUDED.updated_by,
		        updated_at  = now(),
		        -- Название партии могло измениться, но пустым его не затираем.
		        title = CASE WHEN EXCLUDED.title = '' THEN supremacy_game_tasks.title
		                     ELSE EXCLUDED.title END`,
		gameID, title, on, actorID)
	return err
}

// HeroDeployEnabled перечисляет партии, где автопризыв включён.
func (r *GameTasks) HeroDeployEnabled(ctx context.Context) ([]domain.GameTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT game_id, title FROM supremacy_game_tasks WHERE hero_deploy ORDER BY game_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.GameTask
	for rows.Next() {
		var t domain.GameTask
		if err := rows.Scan(&t.GameID, &t.Title); err != nil {
			return nil, err
		}
		t.HeroDeploy = true
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkHeroRun записывает, чем кончился заход воркера в партию.
func (r *GameTasks) MarkHeroRun(ctx context.Context, gameID string, at time.Time, result string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_game_tasks SET last_run_at = $2, last_result = $3 WHERE game_id = $1`,
		gameID, at, result)
	return err
}
