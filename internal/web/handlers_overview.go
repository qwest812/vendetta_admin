package web

import (
	"net/http"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// Сколько строк в списках «Обзора»: страница — сводка, полные списки
// живут в своих разделах.
const (
	overviewAllies  = 6
	overviewGames   = 6
	overviewChecked = 5
	overviewListed  = 6
)

// listedView — личный список для «Обзора»: первые записи и сколько всего.
type listedView struct {
	Items []domain.Relation
	Total int
}

func cutListed(list []domain.Relation) listedView {
	v := listedView{Items: list, Total: len(list)}
	if len(list) > overviewListed {
		v.Items = list[:overviewListed]
	}
	return v
}

// pct — доля в процентах, без дробей: «сильнее 84% базы».
func pct(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// overview — главная после входа: что база знает о самом смотрящем.
// Всё здесь собрано само — счёт с сайта игры, архив коалиций, альянс, —
// поэтому страница работает и при выключенных ручных разделах; личные
// списки показываются, только пока они включены.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := currentUser(r)
	data := map[string]any{}

	if s.checked != nil && me.CanViewGames() {
		list, err := s.checked.List(ctx, me.ID, time.Now().Add(-domain.CheckedGamesTTL))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(list) > overviewChecked {
			list = list[:overviewChecked]
		}
		data["Checked"] = list
		if !me.IsRoot() && s.checks != nil {
			left, err := s.checks.Left(ctx, me.ID, me.MapChecks, repo.Day(time.Now()))
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			data["ChecksLeft"], data["ChecksTotal"], data["ChecksCounted"] = left, me.MapChecks, true
		}
	}

	// Враги и друзья — каждый своим блоком. Это ручной раздел: выключен
	// он — блоков нет.
	if s.manualOn(ctx) {
		enemies, err := s.enemies.repo.List(ctx, me.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		friends, err := s.friends.repo.List(ctx, me.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Lists"] = true
		data["EnemyList"], data["FriendList"] = cutListed(enemies), cutListed(friends)
	}

	// Без игрового ID в профиле сравнивать некого: просим его указать.
	if me.GameID == "" || s.overviewRepo == nil {
		data["NoGameID"] = me.GameID == ""
		s.render(w, r, http.StatusOK, "overview", data)
		return
	}

	stats, err := s.freshStats(ctx, []string{me.GameID})
	if err != nil {
		// Сайт игры мог не ответить — обзор показывает остальное.
		s.log.Warn("обзор: счёт игрока", "game_id", me.GameID, "err", err)
	}
	a, err := s.analytics(ctx, me.GameID, stats[me.GameID])
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["A"] = a
	if a.Alliance != nil {
		data["Alliance"] = a.Alliance
	}
	if a.Rated {
		data["Stats"] = a.Stats
	}

	s.render(w, r, http.StatusOK, "overview", data)
}
