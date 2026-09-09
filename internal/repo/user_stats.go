package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// UserStats — боевой счёт игроков с сайта игры. Таблица работает кешем:
// страница партии читает отсюда, а спрашивает сайт только про тех, чья
// запись устарела, — их на открытой карте единицы.
type UserStats struct{ pool *pgxpool.Pool }

func NewUserStats(pool *pgxpool.Pool) *UserStats { return &UserStats{pool: pool} }

// Known отдаёт всё, что о переданных игроках известно, включая записи,
// которые пора обновить: вчерашний счёт лучше серой карты.
func (r *UserStats) Known(ctx context.Context, siteUserIDs []string) (map[string]domain.UserStats, error) {
	found := make(map[string]domain.UserStats, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return found, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT site_user_id, level, defeated, casualties, games,
		        solo_wins, coalition_wins, overall_score, checked_at
		   FROM supremacy_user_stats WHERE site_user_id = ANY($1)`, siteUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var s domain.UserStats
		if err := rows.Scan(&s.SiteUserID, &s.Level, &s.Defeated, &s.Casualties,
			&s.Games, &s.SoloWins, &s.CoalitionWins, &s.OverallScore, &s.CheckedAt); err != nil {
			return nil, err
		}
		found[s.SiteUserID] = s
	}
	return found, rows.Err()
}

// Save кладёт ответы сайта. Время проверки обновляется всегда, даже когда
// счёт не изменился: смысл записи — «на этот момент было так», и по нему
// страница решает, пора ли спрашивать заново.
//
// Повторы в списке убираем заранее: ON CONFLICT DO UPDATE не правит одну
// строку дважды в одном запросе, и пакетная запись упала бы целиком.
func (r *UserStats) Save(ctx context.Context, list []domain.UserStats, at time.Time) error {
	users := make([]string, 0, len(list))
	levels, games, solo, coal := []int32{}, []int32{}, []int32{}, []int32{}
	defeated, casualties, score := []int64{}, []int64{}, []int64{}
	seen := make(map[string]int, len(list))
	for _, s := range list {
		if s.SiteUserID == "" {
			continue
		}
		i, ok := seen[s.SiteUserID]
		if !ok {
			i = len(users)
			seen[s.SiteUserID] = i
			users = append(users, s.SiteUserID)
			levels, games, solo, coal = append(levels, 0), append(games, 0), append(solo, 0), append(coal, 0)
			defeated, casualties, score = append(defeated, 0), append(casualties, 0), append(score, 0)
		}
		levels[i] = int32(s.Level)
		games[i], solo[i], coal[i] = int32(s.Games), int32(s.SoloWins), int32(s.CoalitionWins)
		defeated[i], casualties[i], score[i] = s.Defeated, s.Casualties, s.OverallScore
	}
	if len(users) == 0 {
		return nil
	}

	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_user_stats
		     (site_user_id, level, defeated, casualties, games,
		      solo_wins, coalition_wins, overall_score, checked_at)
		 SELECT u.site_user_id, u.level, u.defeated, u.casualties, u.games,
		        u.solo_wins, u.coalition_wins, u.overall_score, $9
		   FROM unnest($1::text[], $2::int[], $3::bigint[], $4::bigint[], $5::int[],
		               $6::int[], $7::int[], $8::bigint[])
		        AS u(site_user_id, level, defeated, casualties, games,
		             solo_wins, coalition_wins, overall_score)
		 ON CONFLICT (site_user_id) DO UPDATE SET
		     level = EXCLUDED.level,
		     defeated = EXCLUDED.defeated,
		     casualties = EXCLUDED.casualties,
		     games = EXCLUDED.games,
		     solo_wins = EXCLUDED.solo_wins,
		     coalition_wins = EXCLUDED.coalition_wins,
		     overall_score = EXCLUDED.overall_score,
		     checked_at = EXCLUDED.checked_at`,
		users, levels, defeated, casualties, games, solo, coal, score, at)
	return err
}
