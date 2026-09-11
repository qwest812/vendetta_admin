package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Players struct{ pool *pgxpool.Pool }

func NewPlayers(pool *pgxpool.Pool) *Players { return &Players{pool: pool} }

const playerSelect = `
	SELECT p.id, coalesce(p.game_id, ''), p.nickname, p.clan_id, coalesce(c.name, ''),
	       coalesce(c.status, ''), p.created_by, p.created_at, p.updated_at
	FROM players p LEFT JOIN clans c ON c.id = p.clan_id`

func scanPlayer(row pgx.Row) (*domain.Player, error) {
	var p domain.Player
	err := row.Scan(&p.ID, &p.GameID, &p.Nickname, &p.ClanID, &p.ClanName, &p.ClanStatus,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Search ищет по подстроке ника или игрового ID без учёта регистра. Пустой
// запрос отдаёт последних добавленных — это стартовый экран поиска.
//
// Пустой status — «любой клан». Фильтр смотрит на клан игрока, поэтому
// карточки без клана не попадают ни в один из статусов.
//
// traits — коды признаков, и выбранные складываются, а не заменяют друг
// друга: отметили «врёт» и «мультивод» — получите тех, у кого стоят обе
// отметки. Каждая галочка сужает выборку, как и положено фильтру.
func (r *Players) Search(ctx context.Context, query string, status domain.ClanStatus,
	limit int) ([]*domain.Player, error) {

	query = strings.TrimSpace(query)

	var conds []string
	var args []any
	if query != "" {
		args = append(args, query)
		conds = append(conds, `(p.nickname ILIKE '%' || $1 || '%' OR p.game_id ILIKE '%' || $1 || '%')`)
	}
	if status != "" {
		args = append(args, string(status))
		conds = append(conds, fmt.Sprintf(`c.status = $%d`, len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// Без запроса сортировать не по чему — показываем свежие. С запросом
	// сначала точное совпадение, потом начинающиеся с него, потом остальные.
	// Игровой ID идёт первым: он не меняется, поэтому попадание по нему
	// точнее совпадения по нику.
	order := ` ORDER BY p.updated_at DESC`
	if query != "" {
		order = ` ORDER BY (lower(coalesce(p.game_id, '')) = lower($1)) DESC,
		         (lower(p.nickname) = lower($1)) DESC,
		         (coalesce(p.game_id, '') ILIKE $1 || '%') DESC,
		         (p.nickname ILIKE $1 || '%') DESC,
		         length(p.nickname), p.nickname`
	}

	args = append(args, limit)
	rows, err := r.pool.Query(ctx, playerSelect+where+order+fmt.Sprintf(" LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.collect(ctx, rows)
}

// MarkTrait ставит отметку признака от имени человека. Отметки личные:
// один и тот же признак ставят разные люди, и в карточке напротив него
// стоит их число.
//
// Второе значение — изменилось ли что-то: повторное нажатие (две вкладки,
// двойной клик) не должно попадать в журнал второй раз.
func (r *Players) MarkTrait(ctx context.Context, playerID, traitID, userID int64) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO player_traits (player_id, trait_id, user_id) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`, playerID, traitID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnmarkTrait снимает свою отметку. Чужие не трогает: их снимает админ,
// и для этого есть UnmarkTraitAll.
func (r *Players) UnmarkTrait(ctx context.Context, playerID, traitID, userID int64) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM player_traits WHERE player_id = $1 AND trait_id = $2 AND user_id = $3`,
		playerID, traitID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnmarkTraitAll снимает признак у всех, кто его отметил. Это админское
// средство против наговора: разбираться, кто из пятерых погорячился,
// в карточке негде, а убрать обвинение целиком иногда нужно.
func (r *Players) UnmarkTraitAll(ctx context.Context, playerID, traitID int64) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM player_traits WHERE player_id = $1 AND trait_id = $2`, playerID, traitID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// TraitMarks — признаки игрока со счётчиком: сколько человек отметили
// каждый, отмечал ли его я и кто именно отмечал. Имена нужны только
// админам, но берутся всегда: запрос один, а решает интерфейс.
func (r *Players) TraitMarks(ctx context.Context, playerID, userID int64) ([]domain.TraitMark, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+prefixed(traitColumns, "t")+`, count(*),
		        bool_or(pt.user_id = $2),
		        array_remove(array_agg(u.nickname ORDER BY u.nickname), NULL)
		   FROM player_traits pt
		   JOIN traits t ON t.id = pt.trait_id
		   LEFT JOIN users u ON u.id = pt.user_id
		  WHERE pt.player_id = $1
		  GROUP BY `+prefixed(traitColumns, "t")+`, t.weight
		  ORDER BY (t.weight >= 0), t.sort_order, t.id`, playerID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.TraitMark
	for rows.Next() {
		var m domain.TraitMark
		if err := rows.Scan(&m.ID, &m.Code, &m.Name, &m.Kind, &m.IsActive, &m.SortOrder, &m.CreatedAt,
			&m.Count, &m.Mine, &m.By); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ImportSeen заводит карточки всем, кого встретили в партии: ник и игровой
// ID — всё, что игра рассказывает сама. Возвращает, сколько человек оказалось
// новыми.
//
// Опознаём по игровому ID, поэтому уже заведённых ON CONFLICT просто
// пропускает — ни ник, ни клан, ни признаки существующей карточки заход
// в партию не трогает: там могут быть чужие правки, а мы знаем только
// то, что сказала игра. Переименовавшийся так и остаётся под старым ником
// в карточке, и поправить его — по-прежнему дело админа.
//
// Автор у таких карточек пуст: их завела не рука, а заход в партию.
// Безымянных пропускаем — карточка без ника нечитаема, а ник сайт отдаёт
// не всегда.
func (r *Players) ImportSeen(ctx context.Context, list []domain.GamePlayer) (int, error) {
	ids, nicks := make([]string, 0, len(list)), make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, p := range list {
		if p.SiteUserID == "" || p.Nickname == "" || seen[p.SiteUserID] {
			continue
		}
		seen[p.SiteUserID] = true
		ids = append(ids, p.SiteUserID)
		nicks = append(nicks, p.Nickname)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	tag, err := r.pool.Exec(ctx,
		`INSERT INTO players (game_id, nickname)
		 SELECT * FROM unnest($1::text[], $2::text[])
		 ON CONFLICT (lower(game_id)) DO NOTHING`, ids, nicks)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ByGameIDs находит карточки по игровым ID — так партия сводится с базой.
// Ключи в ответе те же строки, что передали: игровой ID уникален без учёта
// регистра, поэтому и сравнение регистронезависимое.
func (r *Players) ByGameIDs(ctx context.Context, gameIDs []string) (map[string]*domain.Player, error) {
	found := make(map[string]*domain.Player, len(gameIDs))
	if len(gameIDs) == 0 {
		return found, nil
	}

	rows, err := r.pool.Query(ctx,
		playerSelect+` WHERE lower(coalesce(p.game_id, '')) = ANY($1)`, lowerAll(gameIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	players, err := r.collect(ctx, rows)
	if err != nil {
		return nil, err
	}
	byLower := make(map[string]*domain.Player, len(players))
	for _, p := range players {
		byLower[strings.ToLower(p.GameID)] = p
	}
	for _, id := range gameIDs {
		if p, ok := byLower[strings.ToLower(id)]; ok {
			found[id] = p
		}
	}
	return found, nil
}

func lowerAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.ToLower(v)
	}
	return out
}

// ByClan отдаёт состав клана — карточки, привязанные к нему.
func (r *Players) ByClan(ctx context.Context, clanID int64, limit int) ([]*domain.Player, error) {
	rows, err := r.pool.Query(ctx,
		playerSelect+` WHERE p.clan_id = $1 ORDER BY lower(p.nickname) LIMIT $2`, clanID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.collect(ctx, rows)
}

// collect дочитывает выборку и догружает признаки.
func (r *Players) collect(ctx context.Context, rows pgx.Rows) ([]*domain.Player, error) {
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

	// DISTINCT: в списках метка одна на признак, сколько бы человек его
	// ни отметили. Счётчик показывает карточка, см. TraitMarks.
	// Группировка вместо простого выбора: один признак от нескольких людей
	// в списке остаётся одной меткой. Счётчик показывает карточка,
	// см. TraitMarks.
	rows, err := r.pool.Query(ctx,
		`SELECT pt.player_id, `+prefixed(traitColumns, "t")+`
		 FROM player_traits pt JOIN traits t ON t.id = pt.trait_id
		 WHERE pt.player_id = ANY($1)
		 GROUP BY pt.player_id, `+prefixed(traitColumns, "t")+`, t.weight
		 ORDER BY (t.weight >= 0), t.sort_order, t.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var playerID int64
		var t domain.Trait
		if err := rows.Scan(&playerID, &t.ID, &t.Code, &t.Name, &t.Kind, &t.IsActive, &t.SortOrder, &t.CreatedAt); err != nil {
			return err
		}
		if p := byID[playerID]; p != nil {
			p.Traits = append(p.Traits, t)
		}
	}
	return rows.Err()
}

// Create заводит карточку. Пустой игровой ID уходит в NULL: у старых карточек
// его может не быть, и такие записи не должны конфликтовать между собой.
func (r *Players) Create(ctx context.Context, gameID, nickname, clanName string, createdBy int64) (*domain.Player, error) {
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
		`INSERT INTO players (game_id, nickname, clan_id, created_by)
		 VALUES (NULLIF($1, ''), $2, $3, $4) RETURNING id`,
		gameID, nickname, clanID, createdBy).Scan(&id)
	if isUniqueViolation(err, "players_game_id_key") {
		return nil, domain.ErrGameIDTaken
	}
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.ByID(ctx, id)
}

func (r *Players) Update(ctx context.Context, id int64, gameID, nickname, clanName string) error {
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
		`UPDATE players SET game_id = NULLIF($2, ''), nickname = $3, clan_id = $4, updated_at = now()
		 WHERE id = $1`,
		id, gameID, nickname, clanID)
	if isUniqueViolation(err, "players_game_id_key") {
		return domain.ErrGameIDTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
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

// HeroMarks — герои игрока со сводным уровнем: какой назвали чаще других,
// сколько человек отметили и что поставил я. Сводный считается модой,
// а не средним: уровни — это ступени, и «13,5» не бывает.
//
// Порядок ничей: сортирует список карточка, по справочнику героев.
func (r *Players) HeroMarks(ctx context.Context, playerID, userID int64) ([]domain.HeroMark, error) {
	rows, err := r.pool.Query(ctx,
		// mode() берёт самое частое значение, а порядок по убыванию решает
		// ничью в пользу большего уровня: герой качается, а не разучивается.
		`SELECT unit_type_id,
		        mode() WITHIN GROUP (ORDER BY level DESC),
		        count(*),
		        COALESCE(max(level) FILTER (WHERE user_id = $2), 0)
		   FROM player_heroes
		  WHERE player_id = $1
		  GROUP BY unit_type_id`, playerID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.HeroMark
	for rows.Next() {
		var m domain.HeroMark
		if err := rows.Scan(&m.UnitTypeID, &m.Level, &m.Count, &m.Mine); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetHeroLevel ставит мой уровень герою. Ноль означает «героя у игрока
// нет» — строка тогда убирается вовсе, а не сохраняется нулём.
//
// Второе значение — изменилось ли что-то: повторная отправка формы
// не должна попадать в журнал второй раз.
func (r *Players) SetHeroLevel(ctx context.Context, playerID int64, unitTypeID int, userID int64, level int) (bool, error) {
	if level <= 0 {
		tag, err := r.pool.Exec(ctx,
			`DELETE FROM player_heroes
			  WHERE player_id = $1 AND unit_type_id = $2 AND user_id = $3`,
			playerID, unitTypeID, userID)
		if err != nil {
			return false, err
		}
		return tag.RowsAffected() > 0, nil
	}

	tag, err := r.pool.Exec(ctx,
		`INSERT INTO player_heroes (player_id, unit_type_id, user_id, level)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (player_id, unit_type_id, user_id)
		 DO UPDATE SET level = EXCLUDED.level, updated_at = now()
		 WHERE player_heroes.level <> EXCLUDED.level`,
		playerID, unitTypeID, userID, level)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Comments — лента комментариев об игроке: свежие сверху. Порядок по дате
// правки, а не создания: переписанный комментарий — это свежее слово,
// и ему место наверху.
func (r *Players) Comments(ctx context.Context, playerID int64) ([]domain.Comment, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.id, c.player_id, c.author_id, COALESCE(u.nickname, ''), c.body, c.created_at, c.updated_at
		   FROM player_comments c
		   LEFT JOIN users u ON u.id = c.author_id
		  WHERE c.player_id = $1
		  ORDER BY c.updated_at DESC, c.id DESC`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Comment
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(&c.ID, &c.PlayerID, &c.AuthorID, &c.AuthorName,
			&c.Body, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveComment пишет комментарий от имени человека. У каждого он один
// на игрока, поэтому новый текст заменяет прежний — форма об этом
// предупреждает заранее.
func (r *Players) SaveComment(ctx context.Context, playerID, authorID int64, body string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO player_comments (player_id, author_id, body)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (player_id, author_id) WHERE author_id IS NOT NULL
		 DO UPDATE SET body = EXCLUDED.body, updated_at = now()
		 RETURNING id`, playerID, authorID, body).Scan(&id)
	return id, err
}

// CommentByID достаёт один комментарий: по нему проверяется право удаления.
func (r *Players) CommentByID(ctx context.Context, id int64) (domain.Comment, error) {
	var c domain.Comment
	err := r.pool.QueryRow(ctx,
		`SELECT c.id, c.player_id, c.author_id, COALESCE(u.nickname, ''), c.body, c.created_at, c.updated_at
		   FROM player_comments c
		   LEFT JOIN users u ON u.id = c.author_id
		  WHERE c.id = $1`, id).
		Scan(&c.ID, &c.PlayerID, &c.AuthorID, &c.AuthorName, &c.Body, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	return c, err
}

func (r *Players) DeleteComment(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM player_comments WHERE id = $1`, id)
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

// prefixed добавляет алиас таблицы к списку колонок.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ", ")
}
