package repo

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Players struct{ pool *pgxpool.Pool }

func NewPlayers(pool *pgxpool.Pool) *Players { return &Players{pool: pool} }

const playerSelect = `
	SELECT p.id, p.nickname, p.clan_id, coalesce(c.name, ''), p.created_by, p.created_at, p.updated_at
	FROM players p LEFT JOIN clans c ON c.id = p.clan_id`

func scanPlayer(row pgx.Row) (*domain.Player, error) {
	var p domain.Player
	err := row.Scan(&p.ID, &p.Nickname, &p.ClanID, &p.ClanName, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Search ищет по подстроке ника без учёта регистра. Пустой запрос отдаёт
// последних добавленных — это стартовый экран поиска.
func (r *Players) Search(ctx context.Context, query string, limit int) ([]*domain.Player, error) {
	query = strings.TrimSpace(query)

	var rows pgx.Rows
	var err error
	if query == "" {
		rows, err = r.pool.Query(ctx, playerSelect+` ORDER BY p.updated_at DESC LIMIT $1`, limit)
	} else {
		// Сначала точное совпадение, потом начинающиеся с запроса, потом остальные.
		rows, err = r.pool.Query(ctx, playerSelect+`
			WHERE p.nickname ILIKE '%' || $1 || '%'
			ORDER BY (lower(p.nickname) = lower($1)) DESC,
			         (p.nickname ILIKE $1 || '%') DESC,
			         length(p.nickname), p.nickname
			LIMIT $2`, query, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []*domain.Player
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return players, r.attachTraits(ctx, players)
}

func (r *Players) ByID(ctx context.Context, id int64) (*domain.Player, error) {
	p, err := scanPlayer(r.pool.QueryRow(ctx, playerSelect+` WHERE p.id = $1`, id))
	if err != nil {
		return nil, err
	}
	return p, r.attachTraits(ctx, []*domain.Player{p})
}

// attachTraits догружает отметки одним запросом на всю выборку.
func (r *Players) attachTraits(ctx context.Context, players []*domain.Player) error {
	if len(players) == 0 {
		return nil
	}
	ids := make([]int64, len(players))
	byID := make(map[int64]*domain.Player, len(players))
	for i, p := range players {
		ids[i] = p.ID
		byID[p.ID] = p
	}

	rows, err := r.pool.Query(ctx,
		`SELECT pt.player_id, `+prefixed(traitColumns, "t")+`
		 FROM player_traits pt JOIN traits t ON t.id = pt.trait_id
		 WHERE pt.player_id = ANY($1)
		 ORDER BY (t.weight >= 0), t.sort_order, t.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var playerID int64
		var t domain.Trait
		if err := rows.Scan(&playerID, &t.ID, &t.Code, &t.Name, &t.Weight, &t.IsActive, &t.SortOrder, &t.CreatedAt); err != nil {
			return err
		}
		if p := byID[playerID]; p != nil {
			p.Traits = append(p.Traits, t)
		}
	}
	return rows.Err()
}

func (r *Players) Create(ctx context.Context, nickname, clanName string, traitIDs []int64, createdBy int64) (*domain.Player, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	clanID, err := resolveClan(ctx, tx, clanName)
	if err != nil {
		return nil, err
	}

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO players (nickname, clan_id, created_by) VALUES ($1, $2, $3) RETURNING id`,
		nickname, clanID, createdBy).Scan(&id)
	if isUniqueViolation(err, "players_nickname_key") {
		return nil, domain.ErrNickTaken
	}
	if err != nil {
		return nil, err
	}

	if err := replaceTraits(ctx, tx, id, traitIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.ByID(ctx, id)
}

func (r *Players) Update(ctx context.Context, id int64, nickname, clanName string, traitIDs []int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	clanID, err := resolveClan(ctx, tx, clanName)
	if err != nil {
		return err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE players SET nickname = $2, clan_id = $3, updated_at = now() WHERE id = $1`,
		id, nickname, clanID)
	if isUniqueViolation(err, "players_nickname_key") {
		return domain.ErrNickTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}

	if err := replaceTraits(ctx, tx, id, traitIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Players) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM players WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Players) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM players`).Scan(&n)
	return n, err
}

// Clans отдаёт список кланов для подсказки в форме.
func (r *Players) Clans(ctx context.Context) ([]domain.Clan, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name FROM clans ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Clan
	for rows.Next() {
		var c domain.Clan
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Players) Notes(ctx context.Context, playerID int64) ([]domain.Note, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, player_id, author_id, author_email, body, created_at
		 FROM player_notes WHERE player_id = $1 ORDER BY created_at DESC, id DESC`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Note
	for rows.Next() {
		var n domain.Note
		if err := rows.Scan(&n.ID, &n.PlayerID, &n.AuthorID, &n.AuthorEmail, &n.Body, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *Players) AddNote(ctx context.Context, playerID int64, author *domain.User, body string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO player_notes (player_id, author_id, author_email, body)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		playerID, author.ID, author.Email, body).Scan(&id)
	return id, err
}

func (r *Players) NoteByID(ctx context.Context, id int64) (domain.Note, error) {
	var n domain.Note
	err := r.pool.QueryRow(ctx,
		`SELECT id, player_id, author_id, author_email, body, created_at
		 FROM player_notes WHERE id = $1`, id).
		Scan(&n.ID, &n.PlayerID, &n.AuthorID, &n.AuthorEmail, &n.Body, &n.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, domain.ErrNotFound
	}
	return n, err
}

func (r *Players) DeleteNote(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM player_notes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// resolveClan заводит клан по имени, если его ещё нет. Пустое имя означает
// «без клана».
func resolveClan(ctx context.Context, tx pgx.Tx, name string) (*int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id int64
	err := tx.QueryRow(ctx,
		`INSERT INTO clans (name) VALUES ($1)
		 ON CONFLICT (lower(name)) DO UPDATE SET name = clans.name
		 RETURNING id`, name).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// replaceTraits переписывает набор отметок целиком: форма всегда присылает
// полное состояние чекбоксов.
func replaceTraits(ctx context.Context, tx pgx.Tx, playerID int64, traitIDs []int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM player_traits WHERE player_id = $1`, playerID); err != nil {
		return err
	}
	if len(traitIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO player_traits (player_id, trait_id)
		 SELECT $1, unnest($2::bigint[])
		 ON CONFLICT DO NOTHING`, playerID, traitIDs)
	return err
}

// prefixed добавляет алиас таблицы к списку колонок.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ", ")
}
