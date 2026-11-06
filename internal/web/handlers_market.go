package web

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// marketResource — ресурс биржи в выборе заявки: что это и почём сейчас
// самое дешёвое предложение. Best ноль — предложений нет.
type marketResource struct {
	ID     int
	Name   string
	Best   float64
	Offers int
}

// marketResources — ресурсы, которыми торгует биржа партии.
func marketResources(state *supremacy.GameState) []marketResource {
	var out []marketResource
	for _, id := range state.Tradeable() {
		r := marketResource{ID: id, Name: state.ResourceNames[id]}
		for _, o := range state.Asks[id] {
			if o.Player == state.Me {
				continue
			}
			if r.Best == 0 {
				r.Best = o.Price
			}
			r.Offers++
		}
		out = append(out, r)
	}
	return out
}

// marketNameMax — предел длины названия ресурса из формы.
const marketNameMax = 64

// gameMarketPut ставит заявку на ресурс или заменяет прежнюю. В партию
// не ходит: заявку заберёт воркер на ближайшей проверке, через минуту.
func (s *Server) gameMarketPut(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	l := langOf(r)

	// Ресурс приходит «номер|название»; номер бывает нулём (зерно),
	// поэтому idAndName здесь не годится.
	number, name, _ := strings.Cut(r.PostFormValue("resource"), "|")
	resource, errRes := strconv.Atoi(number)
	name = strings.TrimSpace(name)
	budget, errBudget := strconv.Atoi(strings.TrimSpace(r.PostFormValue("budget")))
	price, errPrice := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(r.PostFormValue("price")), ",", "."), 64)
	every, errEvery := strconv.Atoi(r.PostFormValue("every"))
	if errRes != nil || resource < 0 || name == "" || len([]rune(name)) > marketNameMax ||
		errBudget != nil || budget <= 0 || errPrice != nil || price <= 0 || price > 1e6 ||
		errEvery != nil || !slices.Contains(domain.MarketEveryChoices, every) || s.market == nil {
		s.renderGameCard(w, r, gameID, "card-market", l.T("game.market.bad"), "")
		return
	}

	err := s.market.Put(r.Context(), domain.MarketOrder{
		GameID: gameID, Resource: resource, ResourceName: name,
		Budget: budget, MaxPrice: price, Every: every, NextAt: time.Now(),
	}, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "market.order", "game", 0, map[string]any{
		"game": gameID, "resource": name, "budget": budget, "price": price, "every": every})
	s.renderGameCard(w, r, gameID, "card-market", "", l.T("game.market.added", name))
}

func (s *Server) gameMarketRemove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("orderID"), 10, 64)
	if err != nil || s.market == nil {
		s.renderGameCard(w, r, gameID, "card-market", langOf(r).T("game.market.bad"), "")
		return
	}
	if err := s.market.Remove(r.Context(), gameID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderGameCard(w, r, gameID, "card-market", "", "")
}
