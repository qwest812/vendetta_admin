package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Enemies struct{ pool *pgxpool.Pool }

func NewEnemies(pool *pgxpool.Pool) *Enemies { return &Enemies{pool: pool} }

// enemySelect повторяет набор колонок карточки: список врагов показывает те
// же ник, клан и игровой ID, что и поиск, плюс свой комментарий.
const enemySelect = `
	SELECT p.id, coalesce(p.game_id, ''), p.nickname, p.clan_id, coalesce(c.name, ''),
	       coalesce(c.status, ''), e.comment, e.created_at
	FROM user_enemies e
	     JOIN players p ON p.id = e.player_id
	     LEFT JOIN clans c ON c.id = p.clan_id`

// List отдаёт личный список: свежие записи сверху — обычно интересна
// последняя ссора, а не самая давняя.
func (r *Enemies) List(ctx context.Context, userID int64) ([]domain.Enemy, error) {
	rows, err := r.pool.Query(ctx, enemySelect+`
		WHERE e.user_id = $1
		ORDER BY e.created_at DESC, lower(p.nickname)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Enemy
	for rows.Next() {
		var e domain.Enemy
		var p domain.Player
		if err := rows.Scan(&p.ID, &p.GameID, &p.Nickname, &p.ClanID, &p.ClanName,
			&p.ClanStatus, &e.Comment, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Player = &p
		out = append(out, e)
	}
	return out, rows.Err()
}

// Marked отвечает, кто из выборки уже во врагах у пользователя: подбор в
// форме добавления не должен предлагать добавить того, кто уже добавлен.
func (r *Enemies) Marked(ctx context.Context, userID int64, playerIDs []int64) (map[int64]bool, error) {
	marked := map[int64]bool{}
	if len(playerIDs) == 0 {
		return marked, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT player_id FROM user_enemies WHERE user_id = $1 AND player_id = ANY($2)`,
		userID, playerIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		marked[id] = true
	}
	return marked, rows.Err()
}

// Add записывает игрока во враги. Повтор — не ошибка базы, а сообщение
// пользователю: комментарий у записи уже свой, и молча затирать его нельзя.
func (r *Enemies) Add(ctx context.Context, userID, playerID int64, comment string) error {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO user_enemies (user_id, player_id, comment) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, player_id) DO NOTHING`, userID, playerID, comment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEnemyExists
	}
	return nil
}

// SetComment правит комментарий. Чужую запись не тронуть: user_id в условии.
func (r *Enemies) SetComment(ctx context.Context, userID, playerID int64, comment string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE user_enemies SET comment = $3 WHERE user_id = $1 AND player_id = $2`,
		userID, playerID, comment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Enemies) Remove(ctx context.Context, userID, playerID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM user_enemies WHERE user_id = $1 AND player_id = $2`, userID, playerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
