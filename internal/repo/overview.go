package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Overview — запросы страницы «Обзор»: что база знает о самом смотрящем.
// Всё собрано воркером и заходами в партии, руками тут ничего не вводится,
// поэтому и живёт отдельно от разделов, которые ведут люди.
type Overview struct{ pool *pgxpool.Pool }

func NewOverview(pool *pgxpool.Pool) *Overview { return &Overview{pool: pool} }

// Standing — место игрока среди всех, чей боевой счёт есть в базе: сколько
// из Rated слабее его по каждому показателю. Считаются только те, у кого
// есть уровень: без него счёт с сайта пустой, и сравнивать не с чем.
type Standing struct {
	Rated    int
	Level    int // слабее по уровню
	KD       int // по кд
	WinRate  int // по доле побед; у кого партий нет, в счёт не идут
	WinRated int // сколько всего с партиями — база для доли побед
	Defeated int // по числу уничтоженных юнитов
}

// Standing считает место siteUserID. Второе значение false — счёта
// у игрока нет, и места тоже.
//
// Кд считается как domain.UserStats.KD: без потерь — просто число
// уничтоженных, поэтому делитель не меньше единицы.
func (r *Overview) Standing(ctx context.Context, siteUserID string) (Standing, bool, error) {
	var s Standing
	err := r.pool.QueryRow(ctx,
		`WITH s AS (
		     SELECT site_user_id, level, defeated,
		            defeated::float8 / GREATEST(casualties, 1) AS kd,
		            CASE WHEN games > 0
		                 THEN (solo_wins + coalition_wins)::float8 / games END AS wr
		       FROM supremacy_user_stats WHERE level > 0),
		 me AS (SELECT * FROM s WHERE site_user_id = $1)
		 SELECT count(*),
		        count(*) FILTER (WHERE s.level < me.level),
		        count(*) FILTER (WHERE s.kd < me.kd),
		        count(*) FILTER (WHERE s.wr < me.wr),
		        count(s.wr),
		        count(*) FILTER (WHERE s.defeated < me.defeated)
		   FROM s, me`, siteUserID).
		Scan(&s.Rated, &s.Level, &s.KD, &s.WinRate, &s.WinRated, &s.Defeated)
	if err != nil {
		return s, false, err
	}
	// Нет строки «me» — соединение пустое, и count вернул ноль.
	return s, s.Rated > 0, nil
}

// Ally — с кем игрок чаще всего оказывался в одной коалиции.
type Ally struct {
	SiteUserID string
	Login      string
	Games      int
	CardID     int64 // карточка в базе; ноль — карточки нет
}

// Allies — частые союзники по архиву коалиций, самые частые первыми.
func (r *Overview) Allies(ctx context.Context, siteUserID string, limit int) ([]Ally, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT m2.site_user_id, max(m2.login), count(DISTINCT m1.game_id),
		        coalesce(max(p.id), 0)
		   FROM supremacy_coalition_members m1
		   JOIN supremacy_coalition_members m2
		     ON m2.game_id = m1.game_id AND m2.team_id = m1.team_id
		    AND m2.site_user_id <> m1.site_user_id
		   LEFT JOIN players p ON lower(p.game_id) = lower(m2.site_user_id)
		  WHERE m1.site_user_id = $1
		  GROUP BY m2.site_user_id
		  ORDER BY 3 DESC, 2
		  LIMIT $2`, siteUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ally
	for rows.Next() {
		var a Ally
		if err := rows.Scan(&a.SiteUserID, &a.Login, &a.Games, &a.CardID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArchivedGame — партия из архива коалиций, где игрок был в коалиции.
type ArchivedGame struct {
	GameID string
	Title  string
	Day    int
	Done   bool
	Team   string // название коалиции; пустое — игра его не дала
	Seen   time.Time
}

// ArchivedGames — партии игрока из архива коалиций, свежие сверху, и
// сколько их всего.
func (r *Overview) ArchivedGames(ctx context.Context, siteUserID string, limit int) ([]ArchivedGame, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(DISTINCT game_id) FROM supremacy_coalition_members WHERE site_user_id = $1`,
		siteUserID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT m.game_id, coalesce(g.title, ''), coalesce(g.day_of_game, 0),
		        coalesce(g.done, false), coalesce(c.name, ''), m.last_seen
		   FROM supremacy_coalition_members m
		   LEFT JOIN supremacy_watched_games g ON g.game_id = m.game_id
		   LEFT JOIN supremacy_coalitions c ON c.game_id = m.game_id AND c.team_id = m.team_id
		  WHERE m.site_user_id = $1
		  ORDER BY m.last_seen DESC
		  LIMIT $2`, siteUserID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ArchivedGame
	for rows.Next() {
		var g ArchivedGame
		if err := rows.Scan(&g.GameID, &g.Title, &g.Day, &g.Done, &g.Team, &g.Seen); err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// MyAlliance — альянс игрока, сколько его людей в базе и место в топе.
type MyAlliance struct {
	ID      string
	Name    string
	Tag     string
	Members int // игроков этого альянса, о которых знает база
	Rank    int // место в топе; ноль — в топ не входит или топа нет
}

// Alliance — альянс siteUserID. Второе значение false — альянса нет или
// про игрока ещё не спрашивали.
func (r *Overview) Alliance(ctx context.Context, siteUserID string) (MyAlliance, bool, error) {
	var a MyAlliance
	err := r.pool.QueryRow(ctx,
		`SELECT ua.alliance_id, ua.name, ua.tag,
		        (SELECT count(*) FROM supremacy_user_alliances x WHERE x.alliance_id = ua.alliance_id),
		        coalesce((SELECT rank FROM supremacy_top_alliances t WHERE t.alliance_id = ua.alliance_id), 0)
		   FROM supremacy_user_alliances ua
		  WHERE ua.site_user_id = $1 AND ua.alliance_id <> '' AND ua.alliance_id <> '0'`, siteUserID).
		Scan(&a.ID, &a.Name, &a.Tag, &a.Members, &a.Rank)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}
