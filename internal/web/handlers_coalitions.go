package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/supremacy"
)

// coalitionScanToggle — рубильник сбора коалиций. Живёт в «Настройках»:
// это общий переключатель админки, а не свойство раздела «Игры», где
// остались счётчики архива. Рутовый: обход ходит в игру от общего аккаунта
// проекта, и остановить его может понадобиться быстрее, чем пересобрать
// образ.
func (s *Server) coalitionScanToggle(w http.ResponseWriter, r *http.Request) {
	on := r.PostFormValue("on") == "1"
	me := currentUser(r)

	if err := s.settings.SetBool(r.Context(), repo.SettingCoalitionScan, on, me.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("сбор коалиций переключён", "включён", on, "user_id", me.ID)

	// След в журнале: обход ходит в игру от общего аккаунта, и его
	// остановка должна оставлять запись так же, как выдача доступа.
	s.logAuditOn(r, "coalition_scan", "settings", 0, map[string]any{"on": on})

	// Без htmx подменять карточку в странице некому — возвращаемся в раздел.
	if !hx(r) {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	// Состояние перечитывается из базы, а не берётся из того, что просили:
	// показывать надо то, что записалось. Счётчики заодно приезжают свежие.
	view, err := s.coalitionCard(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPartial(w, r, "settings", "coalitions-switch",
		map[string]any{"C": view, "CSRFToken": csrfToken(r)})
}

// coalitionCard собирает состояние архива: включён ли обход и что он уже
// собрал. Одно место на обе страницы — рубильник в «Настройках» и счётчики
// в «Играх» — и на ответ самого рубильника, иначе они разошлись бы.
func (s *Server) coalitionCard(ctx context.Context) (coalitionStatsView, error) {
	on, err := s.settings.CoalitionScanEnabled(ctx)
	if err != nil {
		return coalitionStatsView{}, err
	}
	stats, err := s.coalitions.Stats(ctx)
	if err != nil {
		return coalitionStatsView{}, err
	}
	return coalitionStatsView{On: on, CoalitionStats: stats}, nil
}

// rememberCoalitions кладёт коалиции открытой партии в архив. Состояние
// всё равно перед глазами — пусть оседает: по нему потом и видно, кто с кем
// уже союзничал. Незнакомая партия заодно встаёт в очередь обхода.
//
// Без сведений о партии не пишем ничего: без скорости союз в скоростной
// партии не отличить от союза в двухмесячной, а это половина смысла.
func (s *Server) rememberCoalitions(ctx context.Context, g *supremacy.Game, state *supremacy.GameState) {
	if s.coalitions == nil || g == nil || state == nil {
		return
	}
	teams := supremacy.CoalitionsOf(state)
	if len(teams) == 0 {
		return
	}
	now := time.Now()
	err := s.coalitions.Observe(ctx, domain.WatchedGame{
		GameID:      state.GameID,
		Title:       g.Title,
		Language:    g.Language,
		Speed:       g.Speed(),
		StartedAt:   g.Started(),
		State:       g.State,
		NextCheckAt: supremacy.FirstCheck(g.Speed(), now),
	}, teams, now)
	if err != nil {
		s.log.Error("запись коалиций партии", "gameID", state.GameID, "err", err)
	}
}

// coalitionPairView — двое, уже состоявшие в одной коалиции, готовые
// к показу: люди, а не номера на сайте игры.
type coalitionPairView struct {
	A, B coalitionSideView
	// Total — во скольких прошлых партиях они были вместе, BySpeed —
	// то же с разбивкой по скорости: союз в четырёхдневной скоростной
	// партии и союз в двухмесячной стоят разного.
	Total   int
	BySpeed []coalitionSpeedView
	// Games — номера партий для ссылок. Показываем не все: пара, игравшая
	// вместе двадцать раз, понятна и по первым.
	Games []string
}

// coalitionSideView — одна сторона пары.
type coalitionSideView struct {
	Nation string
	Name   string
	Card   *domain.Player
	Enemy  bool
	Friend bool
}

// coalitionSpeedView — «x4 — 3 партии».
type coalitionSpeedView struct {
	Speed int
	Games int
}

// coalitionPairs выясняет, кто из игроков этой партии уже союзничал раньше.
// Смотрим только на тех, кто здесь и сейчас: старые связи чужих людей
// на этой странице никому не нужны, а список вышел бы бесконечным.
func (s *Server) coalitionPairs(r *http.Request, state *supremacy.GameState,
	sides *gameSides) ([]coalitionPairView, error) {

	if s.coalitions == nil || state == nil {
		return nil, nil
	}

	// Игрок партии по его номеру на сайте: ответ архива приходит именно
	// в этих номерах, а показать надо страну и ник.
	byUser := make(map[string]supremacy.Player, len(state.Players))
	ids := make([]string, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID == "" || p.IsAI {
			continue
		}
		byUser[p.SiteUserID] = p
		ids = append(ids, p.SiteUserID)
	}
	if len(ids) < 2 {
		return nil, nil
	}

	links, err := s.coalitions.Partners(r.Context(), ids, state.GameID)
	if err != nil {
		return nil, err
	}

	// Архив отвечает строкой на пару и скорость; на странице пара нужна
	// одна, поэтому строки одной пары складываем.
	type key struct{ a, b string }
	merged := map[key]*coalitionPairView{}
	order := []key{}
	for _, l := range links {
		k := key{l.A, l.B}
		v, ok := merged[k]
		if !ok {
			v = &coalitionPairView{
				A: s.coalitionSide(byUser[l.A], sides),
				B: s.coalitionSide(byUser[l.B], sides),
			}
			merged[k] = v
			order = append(order, k)
		}
		v.Total += l.Games
		v.BySpeed = append(v.BySpeed, coalitionSpeedView{Speed: int(l.Speed + 0.5), Games: l.Games})
		for _, id := range l.Sample {
			if len(v.Games) < 5 {
				v.Games = append(v.Games, id)
			}
		}
	}

	out := make([]coalitionPairView, 0, len(order))
	for _, k := range order {
		v := merged[k]
		// Внутри пары скорости от быстрых к медленным: скоростных партий
		// больше, и начинать с них честнее.
		sort.Slice(v.BySpeed, func(i, j int) bool { return v.BySpeed[i].Speed > v.BySpeed[j].Speed })
		out = append(out, *v)
	}
	// Сначала те, кто играл вместе чаще: это и есть самое интересное.
	// При равенстве — по имени, чтобы список не прыгал от захода к заходу.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].A.Nation < out[j].A.Nation
	})
	return out, nil
}

// coalitionSide подписывает одну сторону пары: страна, ник и карточка,
// если она у нас есть.
func (s *Server) coalitionSide(p supremacy.Player, sides *gameSides) coalitionSideView {
	v := coalitionSideView{Nation: p.Nation, Name: p.Name}
	if sides == nil {
		return v
	}
	if card, ok := sides.Cards[p.ID]; ok {
		v.Card = card
	}
	_, v.Enemy = sides.Enemies[p.ID]
	_, v.Friend = sides.Friends[p.ID]
	return v
}

// coalitionStatsView — сводка архива для раздела «Игры».
type coalitionStatsView struct {
	On bool
	domain.CoalitionStats
}

// Speed печатается в шаблоне как «x4»; число хранится дробным, потому что
// у игры оно такое, но показывать «x4.0» незачем.
func (v coalitionSpeedView) Label() string { return "x" + strconv.Itoa(v.Speed) }
