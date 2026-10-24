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
		t             domain.GameTask
		lastRun       *time.Time
		buildNext     *time.Time
		buildRun      *time.Time
		rallyRun      *time.Time
		rallyProvince *int
		updatedBy     *int64
	)
	err := r.pool.QueryRow(ctx,
		`SELECT game_id, title, hero_deploy, updated_by, updated_at, last_run_at, last_result,
		        build_next_at, build_run_at, build_result,
		        rally_province, rally_city, rally_auto, rally_known, rally_run_at, rally_result
		   FROM supremacy_game_tasks WHERE game_id = $1`, gameID).
		Scan(&t.GameID, &t.Title, &t.HeroDeploy, &updatedBy, &t.UpdatedAt, &lastRun, &t.LastResult,
			&buildNext, &buildRun, &t.BuildResult,
			&rallyProvince, &t.RallyCity, &t.RallyAuto, &t.RallyKnown, &rallyRun, &t.RallyResult)
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
	if buildNext != nil {
		t.BuildNextAt = *buildNext
	}
	if buildRun != nil {
		t.BuildRunAt = *buildRun
	}
	if rallyRun != nil {
		t.RallyRunAt = *rallyRun
	}
	if rallyProvince != nil {
		t.RallyProvince = *rallyProvince
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

// AutopilotTasks перечисляет партии, где автопилоту есть дело: призыв
// пехоты или сбор новых армий в город.
func (r *GameTasks) AutopilotTasks(ctx context.Context) ([]domain.GameTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT game_id, title, hero_deploy,
		        coalesce(rally_province, 0), rally_city, rally_auto, rally_known,
		        last_run_at, rally_run_at
		   FROM supremacy_game_tasks WHERE hero_deploy OR rally_auto ORDER BY game_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.GameTask
	for rows.Next() {
		var t domain.GameTask
		// Время последних заходов нужно воркеру после перезапуска: срок
		// следующего захода он считает от них, а не идёт во все партии сразу.
		var lastRun, rallyRun *time.Time
		if err := rows.Scan(&t.GameID, &t.Title, &t.HeroDeploy,
			&t.RallyProvince, &t.RallyCity, &t.RallyAuto, &t.RallyKnown, &lastRun, &rallyRun); err != nil {
			return nil, err
		}
		if lastRun != nil {
			t.LastRunAt = *lastRun
		}
		if rallyRun != nil {
			t.RallyRunAt = *rallyRun
		}
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

// SetRallyCity выбирает, куда собирать войска. Смена города сбор
// не включает и не выключает.
func (r *GameTasks) SetRallyCity(ctx context.Context, gameID, title string, province int, city string, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_game_tasks (game_id, title, rally_province, rally_city, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (game_id) DO UPDATE
		    SET rally_province = EXCLUDED.rally_province,
		        rally_city     = EXCLUDED.rally_city,
		        updated_by     = EXCLUDED.updated_by,
		        updated_at     = now(),
		        title = CASE WHEN EXCLUDED.title = '' THEN supremacy_game_tasks.title
		                     ELSE EXCLUDED.title END`,
		gameID, title, province, city, actorID)
	return err
}

// SetRallyAuto включает и выключает сбор новых армий автопилотом.
// Список виденных армий при этом забывается: «новые» считаются от
// включения, а не от прошлого раза, когда сбор был включён.
func (r *GameTasks) SetRallyAuto(ctx context.Context, gameID string, on bool, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_game_tasks
		    SET rally_auto = $2, rally_known = NULL, updated_by = $3, updated_at = now()
		  WHERE game_id = $1`,
		gameID, on, actorID)
	return err
}

// MarkRallyRun записывает итог сбора. armies — наши армии на момент
// захода; nil означает «в партию не заглянули», и тогда прежний список
// остаётся как был.
func (r *GameTasks) MarkRallyRun(ctx context.Context, gameID string, at time.Time, result string, armies []int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_game_tasks
		    SET rally_run_at = $2, rally_result = $3,
		        rally_known = CASE WHEN $4::bigint[] IS NULL THEN rally_known ELSE $4::bigint[] END
		  WHERE game_id = $1`,
		gameID, at, result, armies)
	return err
}
