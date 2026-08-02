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

const traitColumns = `id, code, name, weight, is_active, sort_order, created_at`

// traitOrder: сначала минусы (по возрастанию веса — самые тяжёлые сверху),
// потом плюсы, внутри — по sort_order.
const traitOrder = `ORDER BY (weight >= 0), sort_order, id`

func scanTrait(row pgx.Row) (domain.Trait, error) {
	var t domain.Trait
	err := row.Scan(&t.ID, &t.Code, &t.Name, &t.Weight, &t.IsActive, &t.SortOrder, &t.CreatedAt)
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

func (r *Traits) ByID(ctx context.Context, id int64) (domain.Trait, error) {
	return scanTrait(r.pool.QueryRow(ctx, `SELECT `+traitColumns+` FROM traits WHERE id = $1`, id))
}

func (r *Traits) Create(ctx context.Context, code, name string, weight, sortOrder int) (domain.Trait, error) {
	t, err := scanTrait(r.pool.QueryRow(ctx,
		`INSERT INTO traits (code, name, weight, sort_order) VALUES ($1, $2, $3, $4)
		 RETURNING `+traitColumns, code, name, weight, sortOrder))
	if isUniqueViolation(err, "traits_code_key") {
		return t, domain.ErrCodeTaken
	}
	return t, err
}

func (r *Traits) Update(ctx context.Context, id int64, name string, weight, sortOrder int, isActive bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE traits SET name = $2, weight = $3, sort_order = $4, is_active = $5 WHERE id = $1`,
		id, name, weight, sortOrder, isActive)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete убирает признак вместе со всеми отметками у игроков — необратимо,
// поэтому доступно только руту.
func (r *Traits) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM traits WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UsageCount — у скольких игроков отмечен признак. Нужен, чтобы предупредить
// перед удалением.
func (r *Traits) UsageCount(ctx context.Context) (map[int64]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT trait_id, count(*) FROM player_traits GROUP BY trait_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
