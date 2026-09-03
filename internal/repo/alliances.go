package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// Alliances — кланы игроков в самой Supremacy. Таблица работает и как кеш,
// и как очередь: страница кладёт сюда встреченных игроков, а фоновый воркер
// разбирает тех, о ком ещё не спрашивали или спрашивали давно. Поэтому
// открытие карты в сеть не ходит вовсе.
type Alliances struct{ pool *pgxpool.Pool }

func NewAlliances(pool *pgxpool.Pool) *Alliances { return &Alliances{pool: pool} }

// Known отдаёт всё, что о переданных игроках известно, — включая записи,
// которые пора обновить. Свежесть здесь не важна: показать вчерашний клан
// лучше, чем серую карту, а обновит запись воркер.
func (r *Alliances) Known(ctx context.Context, siteUserIDs []string) (map[string]domain.Alliance, error) {
	found := make(map[string]domain.Alliance, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return found, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT site_user_id, alliance_id, name, tag, checked_at
		   FROM supremacy_user_alliances WHERE site_user_id = ANY($1)`, siteUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var a domain.Alliance
		if err := rows.Scan(&a.SiteUserID, &a.ID, &a.Name, &a.Tag, &a.CheckedAt); err != nil {
			return nil, err
		}
		found[a.SiteUserID] = a
	}
	return found, rows.Err()
}

// Enqueue записывает встреченных игроков, о которых ещё не спрашивали.
// Уже известных не трогает: их время проверки принадлежит воркеру.
func (r *Alliances) Enqueue(ctx context.Context, siteUserIDs []string) error {
	if len(siteUserIDs) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_user_alliances (site_user_id) SELECT unnest($1::text[])
		 ON CONFLICT (site_user_id) DO NOTHING`, siteUserIDs)
	return err
}

// Stale выдаёт воркеру очередную порцию работы: сначала те, о ком ещё
// не спрашивали, потом самые давние. Лимит обязателен — за один тик воркер
// делает столько запросов к сайту игры, сколько получил номеров.
func (r *Alliances) Stale(ctx context.Context, olderThan time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT site_user_id FROM supremacy_user_alliances
		  WHERE checked_at IS NULL OR checked_at < $1
		  ORDER BY checked_at NULLS FIRST
		  LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EnqueueAlliances записывает встреченные кланы, состав которых ещё
// не забирали. Вызывается там же, где сохраняются ответы про игроков:
// клан узнаётся именно оттуда.
func (r *Alliances) EnqueueAlliances(ctx context.Context, allianceIDs []string) error {
	if len(allianceIDs) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_alliances (alliance_id) SELECT unnest($1::text[])
		 ON CONFLICT (alliance_id) DO NOTHING`, allianceIDs)
	return err
}

// StaleAlliances выдаёт кланы, состав которых пора забрать: сначала те,
// что не забирали ни разу, потом самые давние.
func (r *Alliances) StaleAlliances(ctx context.Context, olderThan time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT alliance_id FROM supremacy_alliances
		  WHERE checked_at IS NULL OR checked_at < $1
		  ORDER BY checked_at NULLS FIRST
		  LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SaveRoster записывает состав клана: сам клан и всех его участников
// разом. Один запрос к сайту закрывает столько игроков, сколько в клане
// людей, — ради этого всё и затевалось.
//
// Ушедших из клана запись не трогает: состав говорит, кто в клане есть,
// а не кого в нём нет. Такого человека поправит проверка по нему самому,
// когда до него дойдёт очередь.
func (r *Alliances) SaveRoster(ctx context.Context, alliance domain.Alliance,
	members []domain.Alliance, at time.Time) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_alliances (alliance_id, name, tag, checked_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (alliance_id) DO UPDATE SET
		     name = EXCLUDED.name, tag = EXCLUDED.tag, checked_at = EXCLUDED.checked_at`,
		alliance.ID, alliance.Name, alliance.Tag, at); err != nil {
		return err
	}

	for _, m := range members {
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_user_alliances (site_user_id, alliance_id, name, tag, checked_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (site_user_id) DO UPDATE SET
			     alliance_id = EXCLUDED.alliance_id,
			     name = EXCLUDED.name,
			     tag = EXCLUDED.tag,
			     checked_at = EXCLUDED.checked_at`,
			m.SiteUserID, alliance.ID, alliance.Name, alliance.Tag, at); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Save кладёт ответы игры. Время проверки обновляется всегда, даже когда
// клан не изменился: смысл записи — «на этот момент было так».
func (r *Alliances) Save(ctx context.Context, list []domain.Alliance, at time.Time) error {
	for _, a := range list {
		_, err := r.pool.Exec(ctx,
			`INSERT INTO supremacy_user_alliances (site_user_id, alliance_id, name, tag, checked_at)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (site_user_id) DO UPDATE SET
			     alliance_id = EXCLUDED.alliance_id,
			     name = EXCLUDED.name,
			     tag = EXCLUDED.tag,
			     checked_at = EXCLUDED.checked_at`,
			a.SiteUserID, a.ID, a.Name, a.Tag, at)
		if err != nil {
			return err
		}
	}
	return nil
}
