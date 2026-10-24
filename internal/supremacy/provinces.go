package supremacy

import (
	"sort"

	"Vendetta_admin/internal/domain"
)

// ownersOf — кто из живых игроков владеет какой провинцией и сколько их
// у каждого. Боты и ничья земля сюда не попадают: передачи им и от них
// о мультиаккаунтах ничего не говорят. teams — коалиция игрока на сайте.
func ownersOf(st *GameState) (owners map[int]string, counts map[string]int, teams map[string]int) {
	owners, counts, teams = map[int]string{}, map[string]int{}, map[string]int{}
	for _, p := range st.Players {
		if p.SiteUserID != "" && !p.IsAI && p.TeamID > 0 {
			teams[p.SiteUserID] = p.TeamID
		}
	}
	for _, pr := range st.Provinces {
		p, ok := st.Players[pr.Owner]
		if !ok || p.SiteUserID == "" || p.IsAI {
			continue
		}
		owners[pr.ID] = p.SiteUserID
		counts[p.SiteUserID]++
	}
	return owners, counts, teams
}

// transfersBetween — какие провинции сменили хозяина между двумя заходами,
// по парам «от кого — к кому». Пара в одной коалиции считается отдельно:
// союзники друг друга не завоёвывают.
func transfersBetween(prev, cur map[int]string, teams map[string]int) []domain.ProvinceTransfer {
	type key struct {
		from, to string
		same     bool
	}
	agg := map[key]int{}
	for id, was := range prev {
		now, ok := cur[id]
		if !ok || now == was {
			continue
		}
		same := teams[was] > 0 && teams[was] == teams[now]
		agg[key{was, now, same}]++
	}
	out := make([]domain.ProvinceTransfer, 0, len(agg))
	for k, n := range agg {
		out = append(out, domain.ProvinceTransfer{From: k.from, To: k.to, SameTeam: k.same, Provinces: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provinces != out[j].Provinces {
			return out[i].Provinces > out[j].Provinces
		}
		return out[i].From+out[i].To < out[j].From+out[j].To
	})
	return out
}
