package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Clans struct{ pool *pgxpool.Pool }

func NewClans(pool *pgxpool.Pool) *Clans { return &Clans{pool: pool} }

// clanSelect считает игроков клана тем же запросом: количество карточек — это
// первое, что спрашивают о клане, и отдельный запрос на строку тут ни к чему.
const clanSelect = `
	SELECT c.id, c.name, c.status, c.created_at, count(p.id)
	FROM clans c LEFT JOIN players p ON p.clan_id = c.id`

func scanClan(row pgx.Row) (domain.Clan, error) {
	var c domain.Clan
	err := row.Scan(&c.ID, &c.Name, &c.Status, &c.CreatedAt, &c.Players)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	return c, err
}

// List отдаёт кланы: сначала помеченные, потом нейтральные. Ради помеченных
// раздел и заводился, они должны быть сверху.
func (r *Clans) List(ctx context.Context) ([]domain.Clan, error) {
	rows, err := r.pool.Query(ctx, clanSelect+`
		GROUP BY c.id
		ORDER BY (c.status = 'neutral'), c.status, lower(c.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Clan
	for rows.Next() {
		c, err := scanClan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Clans) ByID(ctx context.Context, id int64) (domain.Clan, error) {
	return scanClan(r.pool.QueryRow(ctx, clanSelect+` WHERE c.id = $1 GROUP BY c.id`, id))
}

// Create заводит клан вручную — чтобы пометить альянс до того, как в базе
// появится хоть один его игрок.
func (r *Clans) Create(ctx context.Context, name string, status domain.ClanStatus) (domain.Clan, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO clans (name, status) VALUES ($1, $2) RETURNING id`, name, status).Scan(&id)
	if isUniqueViolation(err, "clans_name_key") {
		return domain.Clan{}, domain.ErrClanTaken
	}
	if err != nil {
		return domain.Clan{}, err
	}
	return r.ByID(ctx, id)
}

func (r *Clans) Update(ctx context.Context, id int64, name string, status domain.ClanStatus) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE clans SET name = $2, status = $3 WHERE id = $1`, id, name, status)
	if isUniqueViolation(err, "clans_name_key") {
		return domain.ErrClanTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete убирает клан; карточки игроков остаются и просто теряют клан —
// за это отвечает ON DELETE SET NULL в схеме.
func (r *Clans) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM clans WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
