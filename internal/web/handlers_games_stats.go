package web

import (
	"context"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// statsFresh — сколько посчитанный боевой счёт считается годным. Неделя:
// счёт набирается партиями, а партия идёт неделями, и на цвет провинции
// разница за несколько дней не влияет. Этот же срок решает, пойдёт ли
// страница спрашивать сайт заново, — чем он длиннее, тем реже открытие
// карты стоит похода в сеть.
const statsFresh = 7 * 24 * time.Hour

// gameAlliances выясняет, кто из игроков партии в каком клане Supremacy.
// В сеть отсюда не ходим совсем: страница читает то, что уже лежит в базе,
// а незнакомых игроков просто записывает в очередь — их спросит воркер.
// Поэтому карта открывается ровно за то же время, что и без кланов, а у
// новой партии первый заход показывает часть игроков серыми.
//
// Второй ответ — сколько игроков партии ещё не спрошено: об этом стоит
// сказать на странице, иначе серый цвет читается как «клана нет».
//
// enqueue — ставить ли незнакомых в очередь. Не ставим, когда состав
// уже едет с сайта: он назовёт кланы всех разом, а воркер спрашивал бы
// тех же людей по одному.
func (s *Server) gameAlliances(ctx context.Context, state *supremacy.GameState,
	roster []supremacy.GameLogin, enqueue bool) (map[int]domain.Alliance, int, error) {

	byUser := make(map[string][]int, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			byUser[p.SiteUserID] = append(byUser[p.SiteUserID], p.ID)
		}
	}
	if len(byUser) == 0 {
		return nil, 0, nil
	}

	ids := make([]string, 0, len(byUser))
	for id := range byUser {
		ids = append(ids, id)
	}

	known, err := s.alliances.Known(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	// Состав, только что взятый у сайта, свежее любой записи в базе
	// и перекрывает её.
	for _, a := range allianceRows(roster, time.Now()) {
		known[a.SiteUserID] = a
	}

	// В очередь попадают те, о ком не сказал ни состав, ни база. После
	// того как кланы приезжают вместе с партией, таких почти не остаётся:
	// разве что игрок пришёл в партию после того, как сайт отдал состав.
	pending := 0
	if enqueue {
		queue := make([]string, 0, len(ids))
		for _, id := range ids {
			if a, ok := known[id]; !ok || !a.Known() {
				pending++
				queue = append(queue, id)
			}
		}
		if err := s.alliances.Enqueue(ctx, queue); err != nil {
			return nil, 0, err
		}
	}

	// Один игрок сайта в партии бывает ровно один раз, но связывать
	// правильнее через список: партия ключуется номером в ней, а не
	// номером на сайте.
	out := make(map[int]domain.Alliance, len(known))
	for userID, a := range known {
		for _, playerID := range byUser[userID] {
			out[playerID] = a
		}
	}
	return out, pending, nil
}

// allianceRows переводит состав партии в записи о кланах. Тега сайт здесь
// не даёт — он приходит только с составом самого клана, — и пустой тег
// затирать чужую находку не должен: об этом знает сама запись в базе.
func allianceRows(roster []supremacy.GameLogin, at time.Time) []domain.Alliance {
	out := make([]domain.Alliance, 0, len(roster))
	for _, l := range roster {
		if !l.Known() {
			continue
		}
		row := domain.Alliance{SiteUserID: l.SiteUserID, CheckedAt: &at}
		if l.InClan() {
			row.ID, row.Name = l.AllianceID, l.AllianceName
		}
		out = append(out, row)
	}
	return out
}

// rememberAlliances кладёт кланы из состава партии в базу и ставит новые
// кланы в очередь на состав. Это главный способ пополнения: одна проверка
// партии приносит кланы всех её участников разом, тогда как воркер
// спрашивает по игроку за раз.
func (s *Server) rememberAlliances(ctx context.Context, roster []supremacy.GameLogin) error {
	rows := allianceRows(roster, time.Now())
	if len(rows) == 0 {
		return nil
	}
	if err := s.alliances.Save(ctx, rows, time.Now()); err != nil {
		return err
	}

	seen := map[string]bool{}
	var clans []string
	for _, r := range rows {
		if r.InClan() && !seen[r.ID] {
			seen[r.ID] = true
			clans = append(clans, r.ID)
		}
	}
	return s.alliances.EnqueueAlliances(ctx, clans)
}

// staleStats — чей счёт пора считать заново: тех, кого не знаем вовсе,
// и тех, кого не считали дольше statsFresh. Порядок сохраняется входной,
// а пустые номера и повторы отсеиваются: у сайта каждый номер стоит
// отдельного запроса, и спрашивать одного дважды незачем.
func staleStats(known map[string]domain.UserStats, ids []string, now time.Time) []string {
	var out []string
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		st, ok := known[id]
		if !ok || !st.Known() || now.Sub(*st.CheckedAt) >= statsFresh {
			out = append(out, id)
		}
	}
	return out
}

// gameStats — боевой счёт участников партии и самого смотрящего. Читается
// из базы, а устаревшее пересчитывается прямо здесь: сайт отвечает про
// игрока за раз, но спрашивать надо только тех, чью запись не обновляли
// больше недели. На уже виденной партии таких единицы, а на новой —
// все, и это единственный случай, когда открытие карты заметно дольше.
//
// Счёт смотрящего идёт в той же пачке: он нужен как точка отсчёта, и
// правило свежести у него то же самое.
//
// Отказ сайта страницу не роняет: покажем то, что уже посчитано, а
// непосчитанные останутся серыми.
func (s *Server) gameStats(ctx context.Context, state *supremacy.GameState,
	viewerID string) (map[int]domain.UserStats, domain.UserStats, error) {

	byUser := make(map[string][]int, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			byUser[p.SiteUserID] = append(byUser[p.SiteUserID], p.ID)
		}
	}

	ids := make([]string, 0, len(byUser)+1)
	for id := range byUser {
		ids = append(ids, id)
	}
	if viewerID != "" {
		ids = append(ids, viewerID)
	}
	if len(ids) == 0 {
		return nil, domain.UserStats{}, nil
	}

	known, err := s.freshStats(ctx, ids)
	if err != nil {
		return nil, domain.UserStats{}, err
	}

	out := make(map[int]domain.UserStats, len(known))
	for userID, st := range known {
		for _, playerID := range byUser[userID] {
			out[playerID] = st
		}
	}
	return out, known[viewerID], nil
}

// freshStats — боевой счёт по номерам на сайте: из базы, а устаревший
// пересчитывается у сайта прямо сейчас. Общий для карты партии в админке
// и для расширения: правило свежести у них одно.
func (s *Server) freshStats(ctx context.Context, ids []string) (map[string]domain.UserStats, error) {
	known, err := s.userStats.Known(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Без аккаунта игры спросить сайт некому — отдаём, что есть в базе.
	if s.games == nil {
		return known, nil
	}
	if stale := staleStats(known, ids, time.Now()); len(stale) > 0 {
		for id, st := range s.refreshStats(ctx, stale) {
			known[id] = st
		}
	}
	return known, nil
}

// refreshStats спрашивает сайт про устаревших и записывает ответ. Молчание
// сайта записывается пустой строкой со временем проверки: так отвечает про
// удалённые аккаунты, и переспрашивать их каждый раз незачем.
func (s *Server) refreshStats(ctx context.Context, ids []string) map[string]domain.UserStats {
	fetched, err := s.games.UserStatsBatch(ctx, ids)
	if err != nil {
		// Часть номеров могла и ответить: что узнали — то и запишем.
		s.log.Warn("счёт игроков у сайта", "спрошено", len(ids), "получено", len(fetched), "err", err)
	}
	if len(fetched) == 0 {
		return nil
	}

	now := time.Now()
	out := make(map[string]domain.UserStats, len(fetched))
	list := make([]domain.UserStats, 0, len(fetched))
	for id, f := range fetched {
		row := domain.UserStats{SiteUserID: id, CheckedAt: &now}
		if f != nil {
			row.Level, row.OverallScore = f.Level, f.OverallScore
			row.Defeated, row.Casualties = f.Defeated, f.Casualties
			row.Games, row.SoloWins, row.CoalitionWins = f.Games, f.SoloWins, f.CoalitionWins
		}
		out[id] = row
		list = append(list, row)
	}
	if err := s.userStats.Save(ctx, list, now); err != nil {
		// Записать не вышло — показать всё равно есть что, а на следующем
		// открытии спросим заново.
		s.log.Error("запись счёта игроков", "err", err)
	}
	return out
}
