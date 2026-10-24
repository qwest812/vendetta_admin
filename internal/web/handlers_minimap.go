package web

import (
	"context"
	"net/http"
	"sort"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// myGamesWindow — сколько держать партию в «Ваших партиях» после
// последнего снимка. Две недели: дольше обычная партия не живёт.
const myGamesWindow = 14 * 24 * time.Hour

// myGamesLimit — сколько партий показывать в «Обзоре».
const myGamesLimit = 6

// myGameView — партия пользователя для «Обзора». Источник — снимок из
// расширения (свежий, у самого игрока) или последний заход сбора
// коалиций (без расширения, но реже). Ни тот, ни другой в игру ради
// «Обзора» не ходят.
type myGameView struct {
	GameID    string
	Title     string
	Day       int
	Nation    string
	Mine      int // провинций у пользователя
	Total     int // занятых провинций всего
	TakenAt   time.Time
	Teammates int
	// Scanned — по данным обхода: тогда ботов и ничью землю не различить.
	Scanned bool
}

// myGames — «Ваши партии»: снимки из расширения и, для остальных партий,
// последние заходы сбора коалиций по игровому ID из профиля.
func (s *Server) myGames(ctx context.Context, me *domain.User) ([]myGameView, error) {
	var out []myGameView
	have := map[string]bool{}
	if s.snapshots != nil {
		list, err := s.snapshots.Mine(ctx, me.ID, time.Now().Add(-myGamesWindow))
		if err != nil {
			return nil, err
		}
		for _, sn := range list {
			v := myGameView{GameID: sn.GameID, Day: sn.Day, TakenAt: sn.TakenAt, Total: len(sn.Owners)}
			team := 0
			for _, p := range sn.Players {
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
			have[sn.GameID] = true
		}
	}
	if s.provinces != nil && me.GameID != "" {
		list, err := s.provinces.ScannedGames(ctx, me.GameID, myGamesLimit)
		if err != nil {
			return nil, err
		}
		for _, g := range list {
			if have[g.GameID] {
				continue
			}
			v := myGameView{GameID: g.GameID, Title: g.Title, Day: g.Day, Mine: g.Mine, Total: g.Total,
				TakenAt: g.TakenAt, Scanned: true}
			if team := g.Teams[me.GameID]; team > 0 {
				for site, t := range g.Teams {
					if t == team && site != me.GameID {
						v.Teammates++
					}
				}
			}
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TakenAt.After(out[j].TakenAt) })
	if len(out) > myGamesLimit {
		out = out[:myGamesLimit]
	}
	return out, nil
}

// miniShape — провинция мини-карты: очертания и чья она относительно
// смотрящего.
type miniShape struct {
	Points string
	Class  string
}

// miniMap — мини-карта своей партии: вы, ваша коалиция, ваши враги,
// остальные. Отдаётся отдельно, по запросу из «Обзора»: очертания
// тяжёлые, и ждать их, открывая главную, незачем. Только свои партии:
// по своему снимку из расширения или там, где у вас есть провинции
// по данным обхода.
func (s *Server) miniMap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := currentUser(r)
	gameID := r.PathValue("id")
	if !digitsOnly(gameID) || s.games == nil {
		http.NotFound(w, r)
		return
	}

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

	// owner — класс провинции по её номеру; mapID — чьи очертания брать.
	var mapID string
	classOf := map[int]string{}
	scanned := false

	snapOK := false
	if s.snapshots != nil {
		snap, ok, err := s.snapshots.MineOne(ctx, me.ID, gameID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if ok {
			snapOK, mapID = true, snap.MapID
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
			for _, o := range snap.Owners {
				if k, ok := kind[o[1]]; ok {
					classOf[o[0]] = k
				}
			}
		}
	}
	if !snapOK {
		if s.provinces == nil || me.GameID == "" {
			http.NotFound(w, r)
			return
		}
		owners, g, ok, err := s.provinces.ScannedOwners(ctx, gameID, me.GameID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		mapID, scanned = g.MapID, true
		team := g.Teams[me.GameID]
		for id, site := range owners {
			switch {
			case site == me.GameID:
				classOf[id] = "mm-me"
			case enemies[site]:
				classOf[id] = "mm-enemy"
			case team > 0 && g.Teams[site] == team:
				classOf[id] = "mm-team"
			default:
				classOf[id] = "mm-human"
			}
		}
	}

	geo, err := s.games.MapGeometry(ctx, mapID)
	if err != nil {
		s.log.Warn("мини-карта: очертания", "mapID", mapID, "err", err)
		s.renderPartial(w, r, "overview", "minimap", map[string]any{"Error": true})
		return
	}
	shapes, counts := miniShapes(geo, classOf)
	s.renderPartial(w, r, "overview", "minimap", map[string]any{
		"Width": geo.Width, "Height": geo.Height, "Shapes": shapes, "Counts": counts, "Scanned": scanned,
	})
}

// miniShapes раскладывает очертания по классам; провинции без класса —
// ничья земля (а по данным обхода — ещё и боты).
func miniShapes(geo *supremacy.MapGeometry, classOf map[int]string) ([]miniShape, map[string]int) {
	ids := make([]int, 0, len(geo.Land))
	for id := range geo.Land {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	shapes := make([]miniShape, 0, len(ids))
	counts := map[string]int{}
	for _, id := range ids {
		class, ok := classOf[id]
		if !ok {
			class = "mm-free"
		}
		counts[class]++
		shapes = append(shapes, miniShape{Points: svgPoints(geo.Land[id]), Class: class})
	}
	return shapes, counts
}
