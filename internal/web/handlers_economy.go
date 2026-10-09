package web

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// economyView — блок «Экономика и производство» на странице партии:
// суточный баланс каждого игрока по ресурсам и то, что он сейчас
// производит и строит. Считается из того же состояния, что и карта,
// в партию за ним никто не ходит.
type economyView struct {
	Resources []economyResource
	Rows      []economyRow
	// Check — сверка расчёта с игрой по своей стране: расчёт и настоящий
	// прирост из профиля ресурсов. Пусто у наблюдателя.
	Check []economyCheck
}

type economyResource struct {
	ID   int
	Name string
}

type economyRow struct {
	Nation    string
	Name      string
	Mine      bool
	AI        bool
	Provinces int
	Cells     []economyCell
	// Units и Builds — что игрок производит и строит, «название ×N».
	Units  []string
	Builds []string
}

// economyCell — итог по ресурсу и из чего он сложен, для подсказки.
type economyCell struct {
	Net   string
	Neg   bool
	Title string
}

type economyCheck struct {
	Name       string
	Calculated string
	Actual     string
}

// economy собирает блок из состояния партии. meID — страна смотрящего.
func economy(l i18n.Lang, state *supremacy.GameState, meID int) *economyView {
	list := state.Economies()
	if len(list) == 0 {
		return nil
	}

	// Столбцы — ресурсы, которые кто-то добывает или потребляет.
	seen := map[int]bool{}
	for _, e := range list {
		for _, m := range []map[int]float64{e.Output, e.Populace, e.Buildings, e.Army} {
			for id, v := range m {
				if v != 0 {
					seen[id] = true
				}
			}
		}
	}
	v := &economyView{}
	for id := range seen {
		v.Resources = append(v.Resources, economyResource{ID: id, Name: resourceName(state, id)})
	}
	sort.Slice(v.Resources, func(i, j int) bool { return v.Resources[i].ID < v.Resources[j].ID })

	producing, building := productionByOwner(state)
	for _, e := range list {
		row := economyRow{
			Nation: e.Player.Nation, Name: e.Player.Name, AI: e.Player.IsAI,
			Mine: meID > 0 && e.Player.ID == meID, Provinces: e.Provinces,
			Units: producing[e.Player.ID], Builds: building[e.Player.ID],
		}
		if row.Nation == "" {
			row.Nation = fmt.Sprintf("#%d", e.Player.ID)
		}
		for _, r := range v.Resources {
			net := e.Net(r.ID)
			row.Cells = append(row.Cells, economyCell{
				Net: compact(net), Neg: net < 0,
				Title: l.T("game.eco.cell", compact(e.Output[r.ID]), compact(e.Populace[r.ID]),
					compact(e.Buildings[r.ID]), compact(e.Army[r.ID])),
			})
		}
		v.Rows = append(v.Rows, row)

		if len(e.Actual) > 0 {
			for _, r := range v.Resources {
				if actual, ok := e.Actual[r.ID]; ok {
					v.Check = append(v.Check, economyCheck{
						Name: r.Name, Calculated: compact(e.Net(r.ID)), Actual: compact(actual)})
				}
			}
		}
	}
	return v
}

// productionByOwner — что каждый игрок производит и строит, сгруппировано
// по названию: «Пехота ×3», «Казармы 2 ×1».
func productionByOwner(state *supremacy.GameState) (units, builds map[int][]string) {
	unitCount := map[int]map[string]int{}
	buildCount := map[int]map[string]int{}
	add := func(m map[int]map[string]int, owner int, name string) {
		if m[owner] == nil {
			m[owner] = map[string]int{}
		}
		m[owner][name]++
	}
	for _, p := range state.Provinces {
		if p.Owner <= 0 {
			continue
		}
		for _, u := range p.Producing {
			name := state.UnitNames[u.UnitTypeID]
			if name == "" {
				name = fmt.Sprintf("#%d", u.UnitTypeID)
			}
			add(unitCount, p.Owner, name)
		}
		for _, c := range p.Building {
			name := fmt.Sprintf("#%d", c.UpgradeID)
			if up, ok := state.Upgrades[c.UpgradeID]; ok && up.Name != "" {
				name = up.Name
				if up.Tier > 1 {
					name = fmt.Sprintf("%s %d", up.Name, up.Tier)
				}
			}
			add(buildCount, p.Owner, name)
		}
	}
	return countList(unitCount), countList(buildCount)
}

func countList(m map[int]map[string]int) map[int][]string {
	out := make(map[int][]string, len(m))
	for owner, names := range m {
		list := make([]string, 0, len(names))
		for name, n := range names {
			list = append(list, fmt.Sprintf("%s ×%d", name, n))
		}
		sort.Strings(list)
		out[owner] = list
	}
	return out
}

// resourceName — название ресурса: из справочника партии, из своего
// профиля или, на худой конец, номер.
func resourceName(state *supremacy.GameState, id int) string {
	if n := state.ResourceNames[id]; n != "" {
		return n
	}
	if r, ok := state.Resources[id]; ok && r.Name != "" {
		return r.Name
	}
	return fmt.Sprintf("#%d", id)
}

// compact — число коротко: 12 345 → «12.3k», дробное мелкое — без хвоста.
func compact(v float64) string {
	a := math.Abs(v)
	var s string
	switch {
	case a >= 1e6:
		s = fmt.Sprintf("%.1fM", v/1e6)
	case a >= 1e4:
		s = fmt.Sprintf("%.1fk", v/1e3)
	default:
		s = fmt.Sprintf("%.0f", v)
	}
	s = strings.Replace(s, ".0k", "k", 1)
	s = strings.Replace(s, ".0M", "M", 1)
	if s == "-0" {
		s = "0"
	}
	return s
}
