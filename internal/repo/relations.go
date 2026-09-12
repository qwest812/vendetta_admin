package repo

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// relationTables — оба личных списка сразу. Запас карточек у человека общий
// на двоих, поэтому «сколько занято» по одной таблице не посчитать.
//
// Имена таблиц подставляются в SQL строкой, и живут они здесь, рядом
// с конструкторами, — из запроса пользователя сюда не попадает ничего.
var relationTables = []string{"user_enemies", "user_friends"}

// usedSQL — выражение «сколько карточек уже занято», где $1 это user_id.
// Собирается из relationTables, чтобы список таблиц был назван один раз:
// забыть дописать сюда третий список, когда он появится, будет нельзя.
func usedSQL() string {
	parts := make([]string, 0, len(relationTables))
	for _, t := range relationTables {
		parts = append(parts, `(SELECT count(*) FROM `+t+` WHERE user_id = $1)`)
	}
	return strings.Join(parts, " + ")
}

// Relations — личный список игроков: враги или друзья. Списки устроены
// одинаково и различаются только таблицей, поэтому работает с ними один тип.
// Имя таблицы в SQL подставляется строкой, поэтому берётся оно только из
// конструкторов ниже и никогда из запроса пользователя.
type Relations struct {
	pool  *pgxpool.Pool
	table string
}

func NewEnemies(pool *pgxpool.Pool) *Relations {
	return &Relations{pool: pool, table: "user_enemies"}
}

func NewFriends(pool *pgxpool.Pool) *Relations {
	return &Relations{pool: pool, table: "user_friends"}
}

// selectSQL повторяет набор колонок карточки: список показывает те же ник,
// клан и игровой ID, что и поиск, плюс свой комментарий.
func (r *Relations) selectSQL() string {
	return `
	SELECT p.id, coalesce(p.game_id, ''), p.nickname, p.clan_id, coalesce(c.name, ''),
	       coalesce(c.status, ''), rel.comment, rel.created_at
	FROM ` + r.table + ` rel
	     JOIN players p ON p.id = rel.player_id
	     LEFT JOIN clans c ON c.id = p.clan_id`
}

// List отдаёт личный список: свежие записи сверху — обычно интересна
// последняя ссора (или последний союзник), а не самая давняя.
func (r *Relations) List(ctx context.Context, userID int64) ([]domain.Relation, error) {
	rows, err := r.pool.Query(ctx, r.selectSQL()+`
		WHERE rel.user_id = $1
		ORDER BY rel.created_at DESC, lower(p.nickname)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Relation
	for rows.Next() {
		var rel domain.Relation
		var p domain.Player
		if err := rows.Scan(&p.ID, &p.GameID, &p.Nickname, &p.ClanID, &p.ClanName,
			&p.ClanStatus, &rel.Comment, &rel.CreatedAt); err != nil {
			return nil, err
		}
		rel.Player = &p
		out = append(out, rel)
	}
	return out, rows.Err()
}

// Marked отвечает, кто из выборки уже в списке у пользователя: подбор в
// форме добавления не должен предлагать добавить того, кто уже добавлен.
func (r *Relations) Marked(ctx context.Context, userID int64, playerIDs []int64) (map[int64]bool, error) {
	marked := map[int64]bool{}
	if len(playerIDs) == 0 {
		return marked, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT player_id FROM `+r.table+` WHERE user_id = $1 AND player_id = ANY($2)`,
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

// UsedTotal — сколько карточек человек уже занял. Считает оба списка вместе,
// поэтому спрашивать можно любой из них: запас общий, и ответ будет один
// и тот же.
func (r *Relations) UsedTotal(ctx context.Context, userID int64) (int, error) {
	var used int
	err := r.pool.QueryRow(ctx, `SELECT `+usedSQL(), userID).Scan(&used)
	return used, err
}

// Add записывает игрока в список. Повтор — не ошибка базы, а сообщение
// пользователю: комментарий у записи уже свой, и молча затирать его нельзя.
//
// limit — сколько всего карточек человеку можно держать в обоих списках;
// ноль означает «без предела». Предел проверяется здесь и тем же запросом,
// что и вставка: между отдельной проверкой и вставкой успевает вклиниться
// вторая вкладка, и запас уходит в минус.
func (r *Relations) Add(ctx context.Context, userID, playerID int64, comment string, limit int) error {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO `+r.table+` (user_id, player_id, comment)
		 SELECT $1, $2, $3 WHERE $4 = 0 OR `+usedSQL()+` < $4
		 ON CONFLICT (user_id, player_id) DO NOTHING`, userID, playerID, comment, limit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// Строк не прибавилось по одной из двух причин, и человеку они говорят
	// разное: «он уже в списке» и «места кончились». Различаем их вторым
	// запросом — он случается только на неудачном пути.
	var listed bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+r.table+` WHERE user_id = $1 AND player_id = $2)`,
		userID, playerID).Scan(&listed); err != nil {
		return err
	}
	if listed {
		return domain.ErrAlreadyListed
	}
	return domain.ErrPlanLimit
}

// SetComment правит комментарий. Чужую запись не тронуть: user_id в условии.
func (r *Relations) SetComment(ctx context.Context, userID, playerID int64, comment string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE `+r.table+` SET comment = $3 WHERE user_id = $1 AND player_id = $2`,
		userID, playerID, comment)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Relations) Remove(ctx context.Context, userID, playerID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM `+r.table+` WHERE user_id = $1 AND player_id = $2`, userID, playerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
