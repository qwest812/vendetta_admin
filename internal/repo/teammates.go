package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// Teammates — связи «играют вместе», отмеченные руками. Пара хранится
// одной строкой (player_a < player_b), а читается с обеих сторон.
type Teammates struct{ pool *pgxpool.Pool }

func NewTeammates(pool *pgxpool.Pool) *Teammates { return &Teammates{pool: pool} }

// ordered — пара в том порядке, в каком она лежит в таблице.
func ordered(a, b int64) (int64, int64) {
	if a > b {
		return b, a
	}
	return a, b
}

// Of — с кем играет игрок, по нику.
func (r *Teammates) Of(ctx context.Context, playerID int64) ([]domain.Teammate, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT p.id, p.nickname, coalesce(p.game_id, ''), t.note,
		        coalesce(u.nickname, ''), t.added_at
		   FROM player_teammates t
		   JOIN players p ON p.id = CASE WHEN t.player_a = $1 THEN t.player_b ELSE t.player_a END
		   LEFT JOIN users u ON u.id = t.added_by
		  WHERE t.player_a = $1 OR t.player_b = $1
		  ORDER BY lower(p.nickname)`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Teammate
	for rows.Next() {
		var m domain.Teammate
		if err := rows.Scan(&m.PlayerID, &m.Nickname, &m.GameID, &m.Note, &m.AddedBy, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Add связывает двоих. Уже связанных не дублирует — только обновляет
// заметку. Сам с собой игрок не связывается: это отсекает и таблица.
func (r *Teammates) Add(ctx context.Context, a, b int64, note string, by int64) error {
	a, b = ordered(a, b)
	_, err := r.pool.Exec(ctx,
		`INSERT INTO player_teammates (player_a, player_b, note, added_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (player_a, player_b) DO UPDATE SET note = EXCLUDED.note`,
		a, b, note, by)
	return err
}

// Remove развязывает двоих. Связи не было — ErrNotFound.
func (r *Teammates) Remove(ctx context.Context, a, b int64) error {
	a, b = ordered(a, b)
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM player_teammates WHERE player_a = $1 AND player_b = $2`, a, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Among — пары среди игроков с этими номерами на сайте: страница партии
// показывает, кто из её состава, как известно, играет вместе.
func (r *Teammates) Among(ctx context.Context, siteUserIDs []string) ([]domain.TeammatePair, error) {
	if len(siteUserIDs) < 2 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT pa.game_id, pb.game_id, t.note
		   FROM player_teammates t
		   JOIN players pa ON pa.id = t.player_a
		   JOIN players pb ON pb.id = t.player_b
		  WHERE lower(pa.game_id) = ANY($1) AND lower(pb.game_id) = ANY($1)`,
		lowerAll(siteUserIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.TeammatePair
	for rows.Next() {
		var p domain.TeammatePair
		if err := rows.Scan(&p.A, &p.B, &p.Note); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
