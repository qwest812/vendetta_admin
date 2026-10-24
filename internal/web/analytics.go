package web

import (
	"context"
	"fmt"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// analyticsView — что база знает об игроке сама: боевой счёт и место
// в базе, частые союзники и партии по архиву коалиций, альянс. Один вид
// на «Обзор» (про себя) и карточку игрока (про любого), чтобы блоки
// не разошлись.
type analyticsView struct {
	Stats   domain.UserStats
	Rated   bool
	Wins    int
	WinRate int
	// Standing — место в базе в процентах по показателям; nil — сравнить
	// не с чем.
	Standing      map[string]int
	Allies        []repo.Ally
	AlliesMax     int
	Archived      []repo.ArchivedGame
	ArchivedTotal int
	Alliance      *repo.MyAlliance
	// Trend — как игрок вырос за окно trendWindow; nil — истории ещё нет.
	Trend *trendView
}

// trendWindow — за какой срок показывать рост. Месяц: за неделю у
// большинства не меняется ничего, а за год забывается, с чего начинали.
const trendWindow = 30 * 24 * time.Hour

// trendView — разница с самым ранним снимком в окне. Знаки уже расставлены:
// шаблону остаётся показать строку и выбрать цвет по Up.
type trendView struct {
	Days       int
	Level      string
	LevelUp    int // знак: 1 — вырос, -1 — упал, 0 — без изменений
	KD         string
	KDUp       int
	WinRate    string
	WinRateUp  int
	Defeated   string
	DefeatedUp int
}

// sign — знак разницы для цвета: рост зелёный, падение красное.
func sign[T int | int64 | float64](d T) int {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}

// signedNum — «+2», «−3», «0»: минус типографский, как в остальных
// числах страницы.
func signedNum[T int | int64](d T) string {
	switch {
	case d > 0:
		return "+" + groupDigits(int64(d))
	case d < 0:
		return "−" + groupDigits(int64(-d))
	}
	return "0"
}

// trend считает рост относительно снимка. Снимок того же дня, что и
// нынешний счёт, ничего не говорит — тогда роста нет.
func trend(now domain.UserStats, then repo.Snapshot, today time.Time) *trendView {
	ty, tm, td := today.Date()
	sy, sm, sd := then.Day.Date()
	days := int(time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC).Sub(time.Date(sy, sm, sd, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if days <= 0 {
		return nil
	}
	thenKD := float64(then.Defeated)
	if then.Casualties >= 1 {
		thenKD /= float64(then.Casualties)
	}
	dKD := now.KD() - thenKD
	t := &trendView{
		Days:  days,
		Level: signedNum(now.Level - then.Level), LevelUp: sign(now.Level - then.Level),
		KD: fmt.Sprintf("%+.2f", dKD), KDUp: sign(dKD),
		Defeated: signedNum(now.Defeated - then.Defeated), DefeatedUp: sign(now.Defeated - then.Defeated),
	}
	if dKD > -0.005 && dKD < 0.005 {
		t.KD, t.KDUp = "0", 0
	} else if dKD < 0 {
		t.KD = "−" + t.KD[1:]
	}
	if now.Games > 0 && then.Games > 0 {
		nowWR := (now.SoloWins + now.CoalitionWins) * 100 / now.Games
		thenWR := then.Wins * 100 / then.Games
		t.WinRate, t.WinRateUp = signedNum(nowWR-thenWR), sign(nowWR-thenWR)
	}
	return t
}

// Empty — показать нечего: ни счёта, ни архива, ни альянса.
func (a analyticsView) Empty() bool {
	return !a.Rated && len(a.Allies) == 0 && len(a.Archived) == 0 && a.Alliance == nil
}

// analytics собирает вид по игровому ID. Счёт передаётся снаружи: «Обзор»
// освежает его у сайта, а карточка показывает уже собранное и в сеть
// не ходит.
func (s *Server) analytics(ctx context.Context, gameID string, st domain.UserStats) (analyticsView, error) {
	var v analyticsView
	if s.overviewRepo == nil || gameID == "" {
		return v, nil
	}
	if st.Rated() {
		v.Stats, v.Rated = st, true
		v.Wins = st.SoloWins + st.CoalitionWins
		if st.Games > 0 {
			v.WinRate = v.Wins * 100 / st.Games
		}
		standing, ok, err := s.overviewRepo.Standing(ctx, gameID)
		if err != nil {
			return v, err
		}
		if ok {
			v.Standing = map[string]int{
				"Rated":    standing.Rated,
				"Level":    pct(standing.Level, standing.Rated),
				"KD":       pct(standing.KD, standing.Rated),
				"WinRate":  pct(standing.WinRate, standing.WinRated),
				"Defeated": pct(standing.Defeated, standing.Rated),
			}
		}
	}

	// Рост за месяц — по истории снимков; пока её нет, плитки без него.
	if v.Rated && s.userStats != nil {
		snap, ok, err := s.userStats.Since(ctx, gameID, time.Now().Add(-trendWindow))
		if err != nil {
			return v, err
		}
		if ok {
			v.Trend = trend(st, snap, time.Now())
		}
	}

	allies, err := s.overviewRepo.Allies(ctx, gameID, overviewAllies)
	if err != nil {
		return v, err
	}
	v.Allies = allies
	if len(allies) > 0 {
		v.AlliesMax = allies[0].Games
	}

	v.Archived, v.ArchivedTotal, err = s.overviewRepo.ArchivedGames(ctx, gameID, overviewGames)
	if err != nil {
		return v, err
	}

	a, ok, err := s.overviewRepo.Alliance(ctx, gameID)
	if err != nil {
		return v, err
	}
	if ok {
		v.Alliance = &a
	}
	return v, nil
}
