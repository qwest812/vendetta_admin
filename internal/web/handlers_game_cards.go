package web

import (
	"net/http"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// renderGameCard отвечает на кнопку своей партии одной карточкой: htmx
// подменяет только её, и страница остаётся на месте — с картой, раскрытыми
// блоками и местом прокрутки. Без htmx (скрипт не загрузился) рисуется
// вся страница, как раньше.
//
// Состояние партии карточка берёт из копии в памяти, какой бы давности
// она ни была: новый заход ради перерисовки игра засчитала бы как вход.
// Копию освежают и сами кнопки — их заходы кладут свежее состояние туда же.
func (s *Server) renderGameCard(w http.ResponseWriter, r *http.Request, gameID, card, errMsg, notice string) {
	if !hx(r) || s.games == nil {
		s.renderGameNotice(w, r, gameID, false, errMsg, notice)
		return
	}
	data, err := s.gameCardData(r, gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["CardError"], data["CardNotice"], data["Notice"] = errMsg, notice, notice
	s.renderPartial(w, r, "game", card, data)
}

// gameCardData — данные карточек своей партии: настройки и заявки из базы,
// провинции, герои и биржа — из копии состояния, если она есть.
func (s *Server) gameCardData(r *http.Request, gameID string) (map[string]any, error) {
	ctx := r.Context()
	lang := langOf(r)

	task, err := s.tasks.Get(ctx, gameID)
	if err != nil {
		return nil, err
	}
	var heroPlans []domain.HeroPlan
	if s.heroPlans != nil {
		if heroPlans, err = s.heroPlans.List(ctx, gameID); err != nil {
			return nil, err
		}
	}
	var market []domain.MarketOrder
	if s.market != nil {
		if market, err = s.market.List(ctx, gameID); err != nil {
			return nil, err
		}
	}

	data := map[string]any{
		"GameID": gameID, "Task": task, "Game": nil, "HeroPlans": heroPlans,
		"Interval": s.heroEvery, "IntervalMax": s.heroEveryMax,
		"MarketOrders": market, "MarketEvery": domain.MarketEveryChoices,
		"MarketEveryDefault": domain.MarketEveryDefault, "MarketResources": nil,
		"RallyProvinces": nil, "DeployHeroes": nil,
		"Switches": buildingSwitches(lang, nil), "RecruitTodo": -1,
	}
	// Название партии и её скорость — из кэша сайта: название уходит
	// скрытым полем в формы, скорость задаёт паузу автопилота.
	if game, _, _ := s.games.CachedGame(gameID); game != nil {
		view := gameViews(lang, []supremacy.Game{*game})[0]
		data["Game"] = &view
		data["Interval"] = supremacy.AutopilotPause(s.heroEvery, game.Speed())
		data["IntervalMax"] = supremacy.AutopilotPause(s.heroEveryMax, game.Speed())
	}
	if state, ok := s.games.CachedState(gameID); ok && state.Me > 0 {
		if err := s.ownLive(ctx, r, data, state); err != nil {
			return nil, err
		}
	}
	return data, nil
}
