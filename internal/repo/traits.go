package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Traits struct{ pool *pgxpool.Pool }

func NewTraits(pool *pgxpool.Pool) *Traits { return &Traits{pool: pool} }

const traitColumns = `id, code, name, kind, is_active, sort_order, created_at`

// traitOrder: сначала плохое, потом нейтральное, потом хорошее, внутри —
// по sort_order. Плохое сверху не из вредности: за ним и приходят чаще
// всего, а список признаков читают глазами сверху вниз.
const traitOrder = `ORDER BY CASE kind WHEN 'bad' THEN 0 WHEN 'neutral' THEN 1 ELSE 2 END, sort_order, id`

func scanTrait(row pgx.Row) (domain.Trait, error) {
	var t domain.Trait
	err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Kind, &t.IsActive, &t.SortOrder, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	return t, err
}

func (r *Traits) List(ctx context.Context, onlyActive bool) ([]domain.Trait, error) {
	where := ""
	if onlyActive {
		where = "WHERE is_active "
	}
	rows, err := r.pool.Query(ctx, `SELECT `+traitColumns+` FROM traits `+where+traitOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Trait
	for rows.Next() {
		t, err := scanTrait(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ByID — один признак. Нужен обработчику отметок: в журнал пишется название,
// а не номер строки, иначе запись нечитаема.
func (r *Traits) ByID(ctx context.Context, id int64) (domain.Trait, error) {
	return scanTrait(r.pool.QueryRow(ctx, `SELECT `+traitColumns+` FROM traits WHERE id = $1`, id))
}
