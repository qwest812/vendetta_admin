package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// ClientSnapshots — снимки партий из расширения и кто в каких играет.
type ClientSnapshots struct{ pool *pgxpool.Pool }

func NewClientSnapshots(pool *pgxpool.Pool) *ClientSnapshots { return &ClientSnapshots{pool: pool} }

// Save кладёт снимок партии взамен прежнего и отмечает, что пользователь
// в ней играет.
func (r *ClientSnapshots) Save(ctx context.Context, userID int64, s domain.ClientSnapshot) error {
	players, err := json.Marshal(s.Players)
	if err != nil {
		return err
	}
	owners, err := json.Marshal(s.Owners)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_client_snapshots (game_id, map_id, day, players, owners, taken_at, taken_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (game_id) DO UPDATE SET
		     map_id = EXCLUDED.map_id, day = EXCLUDED.day, players = EXCLUDED.players,
		     owners = EXCLUDED.owners, taken_at = EXCLUDED.taken_at, taken_by = EXCLUDED.taken_by`,
		s.GameID, s.MapID, s.Day, players, owners, s.TakenAt, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_client_games (user_id, game_id, player_id, seen_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, game_id) DO UPDATE SET player_id = EXCLUDED.player_id, seen_at = EXCLUDED.seen_at`,
		userID, s.GameID, s.MeID, s.TakenAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Mine — партии пользователя по его снимкам с since, свежие сверху.
// MeID — его номер в партии.
func (r *ClientSnapshots) Mine(ctx context.Context, userID int64, since time.Time) ([]domain.ClientSnapshot, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.game_id, s.map_id, s.day, s.players, s.owners, s.taken_at, g.player_id
		   FROM user_client_games g
		   JOIN supremacy_client_snapshots s ON s.game_id = g.game_id
		  WHERE g.user_id = $1 AND g.seen_at >= $2
		  ORDER BY g.seen_at DESC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ClientSnapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MineOne — одна партия пользователя по его снимку. Чужие снимки сюда
// не попадают: пользователь видит только партии, откуда присылал сам.
func (r *ClientSnapshots) MineOne(ctx context.Context, userID int64, gameID string) (domain.ClientSnapshot, bool, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT s.game_id, s.map_id, s.day, s.players, s.owners, s.taken_at, g.player_id
		   FROM user_client_games g
		   JOIN supremacy_client_snapshots s ON s.game_id = g.game_id
		  WHERE g.user_id = $1 AND g.game_id = $2`, userID, gameID)
	s, err := scanSnapshot(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}

func scanSnapshot(row pgx.Row) (domain.ClientSnapshot, error) {
	var s domain.ClientSnapshot
	var players, owners []byte
	if err := row.Scan(&s.GameID, &s.MapID, &s.Day, &players, &owners, &s.TakenAt, &s.MeID); err != nil {
		return s, err
	}
	if err := json.Unmarshal(players, &s.Players); err != nil {
		return s, err
	}
	if err := json.Unmarshal(owners, &s.Owners); err != nil {
		return s, err
	}
	return s, nil
}
