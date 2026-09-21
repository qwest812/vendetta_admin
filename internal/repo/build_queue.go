package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// BuildQueue — очередь строительства партий: на каждую провинцию свой
// список зданий по порядку. Выключателя у неё нет: пустая очередь и есть
// выключенная, поэтому воркер ходит только туда, где записи есть.
type BuildQueue struct{ pool *pgxpool.Pool }

func NewBuildQueue(pool *pgxpool.Pool) *BuildQueue { return &BuildQueue{pool: pool} }

// List — вся очередь партии: по провинциям, внутри провинции по порядку.
func (r *BuildQueue) List(ctx context.Context, gameID string) ([]domain.BuildEntry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, game_id, province_id, province, upgrade_id, upgrade, position, added_at
		   FROM supremacy_build_queue WHERE game_id = $1
		  ORDER BY province, province_id, position`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.BuildEntry
	for rows.Next() {
		var e domain.BuildEntry
		if err := rows.Scan(&e.ID, &e.GameID, &e.ProvinceID, &e.Province,
			&e.UpgradeID, &e.Upgrade, &e.Position, &e.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Plan — та же очередь в виде, в котором её читает воркер: номера зданий
// по порядку на каждую провинцию.
func (r *BuildQueue) Plan(ctx context.Context, gameID string) (map[int][]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT province_id, upgrade_id FROM supremacy_build_queue
		  WHERE game_id = $1 ORDER BY province_id, position`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plan := map[int][]int{}
	for rows.Next() {
		var province, upgrade int
		if err := rows.Scan(&province, &upgrade); err != nil {
			return nil, err
		}
		plan[province] = append(plan[province], upgrade)
	}
	return plan, rows.Err()
}

// Add дописывает здание в конец очереди провинции.
func (r *BuildQueue) Add(ctx context.Context, e domain.BuildEntry, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_build_queue
		        (game_id, province_id, province, upgrade_id, upgrade, position, added_by)
		 SELECT $1, $2, $3, $4, $5,
		        coalesce(max(position), 0) + 1, $6
		   FROM supremacy_build_queue WHERE game_id = $1 AND province_id = $2`,
		e.GameID, e.ProvinceID, e.Province, e.UpgradeID, e.Upgrade, actorID)
	return err
}

// Remove убирает запись. Партия в условии не для порядка: по номеру записи
// иначе можно было бы выбросить чужую строку, не называя партию.
func (r *BuildQueue) Remove(ctx context.Context, gameID string, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM supremacy_build_queue WHERE id = $1 AND game_id = $2`, id, gameID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Move переставляет запись на шаг вверх или вниз — меняется она местами
// с соседкой по той же провинции. Соседки нет (запись крайняя) — делать
// нечего, и это не ошибка: так ведёт себя всякая стрелка на границе списка.
func (r *BuildQueue) Move(ctx context.Context, gameID string, id int64, up bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var province, position int
	err = tx.QueryRow(ctx,
		`SELECT province_id, position FROM supremacy_build_queue
		  WHERE id = $1 AND game_id = $2 FOR UPDATE`, id, gameID).Scan(&province, &position)
	if err != nil {
		return domain.ErrNotFound
	}

	// Соседка — ближайшая запись выше или ниже по порядку. Позиции идут
	// не подряд (записи убирают), поэтому берётся именно ближайшая.
	order, compare := "DESC", "<"
	if !up {
		order, compare = "ASC", ">"
	}
	var otherID int64
	var otherPos int
	err = tx.QueryRow(ctx,
		`SELECT id, position FROM supremacy_build_queue
		  WHERE game_id = $1 AND province_id = $2 AND position `+compare+` $3
		  ORDER BY position `+order+` LIMIT 1 FOR UPDATE`,
		gameID, province, position).Scan(&otherID, &otherPos)
	if err != nil {
		// Крайнюю запись двигать некуда.
		return nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE supremacy_build_queue SET position = $2 WHERE id = $1`, id, otherPos); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supremacy_build_queue SET position = $2 WHERE id = $1`, otherID, position); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Started убирает из очереди то, что воркер только что поставил в стройку:
// первую запись провинции с этим зданием. Не нашлось — значит, её успели
// убрать руками, и это не ошибка.
func (r *BuildQueue) Started(ctx context.Context, gameID string, provinceID, upgradeID int) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM supremacy_build_queue WHERE id = (
		     SELECT id FROM supremacy_build_queue
		      WHERE game_id = $1 AND province_id = $2 AND upgrade_id = $3
		      ORDER BY position LIMIT 1)`, gameID, provinceID, upgradeID)
	return err
}

// Due — в какие партии воркеру пора зайти: те, где очередь не пуста, а срок
// следующего захода прошёл. Пустой срок означает «заходить при первой
// возможности»: так выглядит только что собранная очередь.
func (r *BuildQueue) Due(ctx context.Context, now time.Time) ([]domain.GameTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT q.game_id, coalesce(t.title, '')
		   FROM supremacy_build_queue q
		   LEFT JOIN supremacy_game_tasks t ON t.game_id = q.game_id
		  WHERE t.build_next_at IS NULL OR t.build_next_at <= $1
		  ORDER BY 1`, now)
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
		out = append(out, t)
	}
	return out, rows.Err()
}

// Hurry назначает заход на сейчас: воркер заберёт партию на ближайшей
// проверке. Итог прошлого захода не трогаем — он виден, пока не придёт новый.
func (r *BuildQueue) Hurry(ctx context.Context, gameID string, now time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_game_tasks (game_id, build_next_at) VALUES ($1, $2)
		 ON CONFLICT (game_id) DO UPDATE SET build_next_at = EXCLUDED.build_next_at`,
		gameID, now)
	return err
}

// MarkRun записывает, чем кончился заход и когда идти снова. Строки партии
// в таблице задач может и не быть: очередь живёт отдельно от кнопки Мейв,
// и заводить её руками для этого не нужно.
func (r *BuildQueue) MarkRun(ctx context.Context, gameID string, at, next time.Time, result string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_game_tasks (game_id, build_run_at, build_next_at, build_result)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (game_id) DO UPDATE
		    SET build_run_at  = EXCLUDED.build_run_at,
		        build_next_at = EXCLUDED.build_next_at,
		        build_result  = EXCLUDED.build_result`,
		gameID, at, next, result)
	return err
}
