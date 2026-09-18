package domain

import (
	"testing"
	"time"
)

func TestCountSignups(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, loc)
	day := func(d, h int) time.Time { return time.Date(2026, 9, d, h, 0, 0, 0, loc) }

	at := []time.Time{
		day(18, 1), day(18, 9), // сегодня, текущая неделя
		day(12, 0),  // первый день текущей недели
		day(11, 23), // последний день прошлой недели
		day(5, 10),  // первый день прошлой недели
		day(4, 10),  // позапрошлая неделя
		// 22:30 по UTC 14-го — это уже 15-е по Москве: сутки считаются
		// в поясе админки, а не базы.
		time.Date(2026, 9, 14, 22, 30, 0, 0, time.UTC),
	}
	st := CountSignups(at, now, 7, loc)

	if st.Current.Count != 4 || st.Previous.Count != 2 {
		t.Fatalf("текущая %d, прошлая %d; ожидалось 4 и 2", st.Current.Count, st.Previous.Count)
	}
	if st.Current.Change != 2 || !st.Current.HasPct || st.Current.ChangePct != 100 {
		t.Errorf("изменение %d (%v), ожидалось +2 и +100%%", st.Current.Change, st.Current.ChangePct)
	}
	if !st.Current.From.Equal(day(12, 0)) || !st.Current.To.Equal(day(18, 0)) {
		t.Errorf("текущая неделя %v–%v", st.Current.From, st.Current.To)
	}
	if len(st.Daily) != 7 || st.Daily[6].Count != 2 || st.Daily[3].Count != 1 || st.MaxDaily != 2 {
		t.Errorf("по дням: %+v, максимум %d", st.Daily, st.MaxDaily)
	}
	if len(st.Periods) != SignupHistory {
		t.Fatalf("отрезков %d, ожидалось %d", len(st.Periods), SignupHistory)
	}
	// Позапрошлая неделя: один против пустой — процента нет, есть только +1.
	if p := st.Periods[2]; p.Count != 1 || p.Change != 1 || p.HasPct {
		t.Errorf("позапрошлая неделя: %+v", p)
	}
	if st.Total != len(at) {
		t.Errorf("всего %d", st.Total)
	}
}
