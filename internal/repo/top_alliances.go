package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// TopAlliances — верхушка рейтинга кланов игры. Это снимок: обход
// переписывает таблицу целиком, истории прошлых снимков не остаётся.
// Читает её страница партии — чтобы отметить на карте игроков из топа,
// а пишет фоновый воркер.
type TopAlliances struct{ pool *pgxpool.Pool }

func NewTopAlliances(pool *pgxpool.Pool) *TopAlliances { return &TopAlliances{pool: pool} }

// All отдаёт весь топ по местам. Пустой ответ — обычное дело: до первого
// обхода топа ещё нет, и карта в этом режиме просто серая.
func (r *TopAlliances) All(ctx context.Context) ([]domain.TopAlliance, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT alliance_id, rank, elo, name, tag, captured_at
		   FROM supremacy_top_alliances ORDER BY rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.TopAlliance
	for rows.Next() {
		var a domain.TopAlliance
		if err := rows.Scan(&a.ID, &a.Rank, &a.Elo, &a.Name, &a.Tag, &a.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CapturedAt — когда топ снимали в последний раз. Ответ nil означает,
// что не снимали ни разу: воркеру этого довольно, чтобы пойти за рейтингом.
func (r *TopAlliances) CapturedAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT max(captured_at) FROM supremacy_top_alliances`).Scan(&at)
	return at, err
}

// Replace кладёт новый снимок вместо старого. Целиком, одной транзакцией:
// топ имеет смысл только как связный список мест, и половина новой десятки
// вперемешку с половиной прошлой не значила бы ничего.
//
// Заодно кланы ставятся в очередь на состав: их игроки — половина смысла
// затеи, а забирает составы уже существующий воркер кланов.
func (r *TopAlliances) Replace(ctx context.Context, list []domain.TopAlliance, at time.Time) error {
	if len(list) == 0 {
		return nil
	}

	ids := make([]string, len(list))
	ranks := make([]int32, len(list))
	elos := make([]int32, len(list))
	names := make([]string, len(list))
	tags := make([]string, len(list))
	for i, a := range list {
		ids[i] = a.ID
		ranks[i] = int32(a.Rank)
		elos[i] = int32(a.Elo)
		names[i] = a.Name
		tags[i] = a.Tag
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Вылетевшие из топа уходят вместе с таблицей: пометка на карте
	// означает «в топе сейчас», и вчерашним местам там делать нечего.
	if _, err := tx.Exec(ctx, `DELETE FROM supremacy_top_alliances`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_top_alliances (alliance_id, rank, elo, name, tag, captured_at)
		 SELECT u.*, $6::timestamptz
		   FROM unnest($1::text[], $2::int[], $3::int[], $4::text[], $5::text[])
		     AS u(alliance_id, rank, elo, name, tag)`,
		ids, ranks, elos, names, tags, at); err != nil {
		return err
	}
	// Состав клана из топа нужен и тогда, когда в партиях он нам ещё
	// не встречался: очередь общая с обычными кланами.
	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_alliances (alliance_id) SELECT unnest($1::text[])
		 ON CONFLICT (alliance_id) DO NOTHING`, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
