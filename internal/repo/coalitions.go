package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// Coalitions — архив коалиций: очередь партий на обход и то, что в них
// нашлось. Пары «кто с кем» здесь не хранятся: это соединение состава
// с самим собой, и считать его на лету дешевле, чем поддерживать.
type Coalitions struct{ pool *pgxpool.Pool }

func NewCoalitions(pool *pgxpool.Pool) *Coalitions { return &Coalitions{pool: pool} }

// Enqueue ставит партию в очередь обхода. Уже известную не трогает совсем:
// у неё своё расписание, и сбивать его появлением в лобби нельзя.
func (r *Coalitions) Enqueue(ctx context.Context, g domain.WatchedGame) error {
	var started *time.Time
	if !g.StartedAt.IsZero() {
		started = &g.StartedAt
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_watched_games
		     (game_id, title, language, speed, started_at, state, next_check_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (game_id) DO NOTHING`,
		g.GameID, g.Title, g.Language, g.Speed, started, g.State, g.NextCheckAt)
	return err
}

// Due отдаёт партии, которым пора, самые просроченные первыми.
func (r *Coalitions) Due(ctx context.Context, at time.Time, limit int) ([]domain.WatchedGame, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT game_id, title, language, speed, started_at, state, day_of_game,
		        next_check_at, checks
		   FROM supremacy_watched_games
		  WHERE NOT done AND next_check_at <= $1
		  ORDER BY next_check_at
		  LIMIT $2`, at, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.WatchedGame
	for rows.Next() {
		var (
			g       domain.WatchedGame
			started *time.Time
		)
		if err := rows.Scan(&g.GameID, &g.Title, &g.Language, &g.Speed, &started,
			&g.State, &g.Day, &g.NextCheckAt, &g.Checks); err != nil {
			return nil, err
		}
		if started != nil {
			g.StartedAt = *started
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Save записывает застигнутые коалиции и двигает расписание партии. Всё
// одной транзакцией: половина состава в базе хуже, чем ничего — по ней
// сложатся пары, которых не было.
//
// Состав пишется интервалом: увидели тех же людей снова — сдвинулся только
// last_seen. Ушедших из коалиции не удаляем, у них так и остаётся прежний
// last_seen: «состояли вместе тогда-то» — это и есть то, что мы знаем.
func (r *Coalitions) Save(ctx context.Context, gameID string, day int,
	teams []domain.Coalition, at, next time.Time, done bool) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := writeCoalitions(ctx, tx, gameID, teams, at); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE supremacy_watched_games
		    SET day_of_game = $2, checked_at = $3, next_check_at = $4,
		        checks = checks + 1, done = $5, last_error = ''
		  WHERE game_id = $1`,
		gameID, day, at, next, done); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail отмечает неудачный заход. Партию не бросаем — срок просто сдвигается:
// игра могла лежать, а второй попытки она стоит.
func (r *Coalitions) Fail(ctx context.Context, gameID, reason string, at, next time.Time, done bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_watched_games
		    SET checked_at = $2, next_check_at = $3, checks = checks + 1,
		        last_error = $4, done = $5
		  WHERE game_id = $1`,
		gameID, at, next, reason, done)
	return err
}

// Observe записывает коалиции, увиденные не обходом, а по случаю: человек
// открыл страницу партии, состояние всё равно перед глазами — пусть оседает.
// Расписание при этом не трогаем: у партии в очереди свой срок, а незнакомую
// заводим с обычным первым сроком, чтобы обход дошёл до неё сам.
func (r *Coalitions) Observe(ctx context.Context, g domain.WatchedGame,
	teams []domain.Coalition, at time.Time) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var started *time.Time
	if !g.StartedAt.IsZero() {
		started = &g.StartedAt
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO supremacy_watched_games
		     (game_id, title, language, speed, started_at, state, next_check_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (game_id) DO NOTHING`,
		g.GameID, g.Title, g.Language, g.Speed, started, g.State, g.NextCheckAt); err != nil {
		return err
	}
	if err := writeCoalitions(ctx, tx, g.GameID, teams, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// writeCoalitions — общая часть обоих путей записи: сам архив.
func writeCoalitions(ctx context.Context, tx pgx.Tx, gameID string,
	teams []domain.Coalition, at time.Time) error {

	for _, team := range teams {
		if _, err := tx.Exec(ctx,
			`INSERT INTO supremacy_coalitions (game_id, team_id, name, first_seen, last_seen)
			 VALUES ($1, $2, $3, $4, $4)
			 ON CONFLICT (game_id, team_id) DO UPDATE SET
			     name = EXCLUDED.name,
			     last_seen = EXCLUDED.last_seen`,
			gameID, team.TeamID, team.Name, at); err != nil {
			return err
		}
		for _, m := range team.Members {
			if _, err := tx.Exec(ctx,
				`INSERT INTO supremacy_coalition_members
				     (game_id, team_id, site_user_id, login, nation, first_seen, last_seen)
				 VALUES ($1, $2, $3, $4, $5, $6, $6)
				 ON CONFLICT (game_id, team_id, site_user_id) DO UPDATE SET
				     login = EXCLUDED.login,
				     nation = EXCLUDED.nation,
				     last_seen = EXCLUDED.last_seen`,
				gameID, team.TeamID, m.SiteUserID, m.Login, m.Nation, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// Partners — кто из переданных игроков уже состоял с кем в одной коалиции.
// Считается соединением состава с самим собой; пара берётся в одном порядке
// (a < b), чтобы не получить каждую дважды.
//
// exceptGameID исключает текущую партию: в ней союз виден и так, а в списке
// «уже играли вместе» он выглядел бы открытием.
func (r *Coalitions) Partners(ctx context.Context, siteUserIDs []string,
	exceptGameID string) ([]domain.CoalitionLink, error) {

	if len(siteUserIDs) < 2 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT m1.site_user_id, m2.site_user_id, g.speed,
		        count(DISTINCT m1.game_id) AS games,
		        (array_agg(DISTINCT m1.game_id))[1:5] AS sample
		   FROM supremacy_coalition_members m1
		   JOIN supremacy_coalition_members m2
		     ON m2.game_id = m1.game_id
		    AND m2.team_id = m1.team_id
		    AND m2.site_user_id > m1.site_user_id
		   JOIN supremacy_watched_games g ON g.game_id = m1.game_id
		  WHERE m1.site_user_id = ANY($1)
		    AND m2.site_user_id = ANY($1)
		    AND m1.game_id <> $2
		  GROUP BY 1, 2, 3
		  ORDER BY games DESC, 1, 2`, siteUserIDs, exceptGameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.CoalitionLink
	for rows.Next() {
		var l domain.CoalitionLink
		if err := rows.Scan(&l.A, &l.B, &l.Speed, &l.Games, &l.Sample); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Stats — что накопил архив. Без счётчика по странице не понять, работает
// обход или стоит.
func (r *Coalitions) Stats(ctx context.Context) (domain.CoalitionStats, error) {
	var (
		s    domain.CoalitionStats
		last *time.Time
	)
	err := r.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE NOT done),
		        count(*) FILTER (WHERE done),
		        max(checked_at)
		   FROM supremacy_watched_games`).
		Scan(&s.Watched, &s.Pending, &s.Done, &last)
	if err != nil {
		return s, err
	}
	if last != nil {
		s.LastCheck = *last
	}
	err = r.pool.QueryRow(ctx,
		`SELECT count(DISTINCT game_id), count(*) FROM supremacy_coalition_members`).
		Scan(&s.Games, &s.Members)
	return s, err
}
