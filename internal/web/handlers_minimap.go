package web

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// myGamesWindow — сколько держать партию в «Ваших партиях» после
// последнего снимка. Две недели: дольше обычная партия не живёт.
const myGamesWindow = 14 * 24 * time.Hour

// myGameView — партия пользователя для «Обзора» по его снимку.
type myGameView struct {
	GameID    string
	Day       int
	Nation    string
	Mine      int // провинций у пользователя
	Total     int // занятых провинций всего
	Humans    int
	TakenAt   time.Time
	Teammates int
}

// myGames — «Ваши партии»: по снимкам, которые расширение присылает
// по кнопке «Анализировать карту». В игру ради этого никто не ходит.
func (s *Server) myGames(ctx context.Context, userID int64) ([]myGameView, error) {
	if s.snapshots == nil {
		return nil, nil
	}
	list, err := s.snapshots.Mine(ctx, userID, time.Now().Add(-myGamesWindow))
	if err != nil {
		return nil, err
	}
	out := make([]myGameView, 0, len(list))
	for _, sn := range list {
		v := myGameView{GameID: sn.GameID, Day: sn.Day, TakenAt: sn.TakenAt, Total: len(sn.Owners)}
		team := 0
		for _, p := range sn.Players {
			if !p.AI {
				v.Humans++
			}
			if p.ID == sn.MeID {
				v.Nation, team = p.Nation, p.Team
			}
		}
		for _, p := range sn.Players {
			if team > 0 && p.Team == team && p.ID != sn.MeID {
				v.Teammates++
			}
		}
		for _, o := range sn.Owners {
			if o[1] == sn.MeID {
				v.Mine++
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// miniShape — провинция мини-карты: очертания и чья она относительно
// смотрящего.
type miniShape struct {
	Points string
	Class  string
}

// miniMap — мини-карта своей партии по снимку из расширения: вы, ваша
// коалиция, ваши враги, остальные люди, боты, ничья земля. Отдаётся
// отдельно, по запросу из «Обзора»: очертания тяжёлые, и ждать их,
// открывая главную, незачем.
func (s *Server) miniMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := currentUser(r)
	gameID := r.PathValue("id")
	if !digitsOnly(gameID) || s.snapshots == nil || s.games == nil {
		http.NotFound(w, r)
		return
	}
	snap, ok, err := s.snapshots.MineOne(ctx, me.ID, gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	geo, err := s.games.MapGeometry(ctx, snap.MapID)
	if err != nil {
		s.log.Warn("мини-карта: очертания", "mapID", snap.MapID, "err", err)
		s.renderPartial(w, r, "overview", "minimap", map[string]any{"Error": true})
		return
	}

	// Враги — из личного списка, пока ручные разделы включены.
	enemies := map[string]bool{}
	if s.manualOn(ctx) {
		list, err := s.enemies.repo.List(ctx, me.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for _, rel := range list {
			if rel.Player.GameID != "" {
				enemies[rel.Player.GameID] = true
			}
		}
	}

	kind := map[int]string{}
	team := 0
	for _, p := range snap.Players {
		if p.ID == snap.MeID {
			team = p.Team
		}
	}
	for _, p := range snap.Players {
		switch {
		case p.ID == snap.MeID:
			kind[p.ID] = "mm-me"
		case p.Site != "" && enemies[p.Site]:
			kind[p.ID] = "mm-enemy"
		case team > 0 && p.Team == team:
			kind[p.ID] = "mm-team"
		case p.AI:
			kind[p.ID] = "mm-ai"
		default:
			kind[p.ID] = "mm-human"
		}
	}
	owner := make(map[int]int, len(snap.Owners))
	for _, o := range snap.Owners {
		owner[o[0]] = o[1]
	}

	ids := make([]int, 0, len(geo.Land))
	for id := range geo.Land {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	shapes := make([]miniShape, 0, len(ids))
	counts := map[string]int{}
	for _, id := range ids {
		class := "mm-free"
		if o, ok := owner[id]; ok {
			if k, ok := kind[o]; ok {
				class = k
			}
		}
		counts[class]++
		shapes = append(shapes, miniShape{Points: svgPoints(geo.Land[id]), Class: class})
	}
	s.renderPartial(w, r, "overview", "minimap", map[string]any{
		"Width": geo.Width, "Height": geo.Height, "Shapes": shapes, "Counts": counts,
		"Enemies": len(enemies) > 0,
	})
}
