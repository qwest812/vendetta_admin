package domain

import "time"

// SignupPeriods — какие отрезки можно выбрать на странице регистраций,
// в днях. SignupDefaultDays — какой открывается сам: неделя слишком
// шумная, а за квартал рост уже прошёл бы незамеченным.
var SignupPeriods = []int{7, 30, 90}

const SignupDefaultDays = 30

// SignupHistory — сколько прошлых отрезков показывать в таблице тенденции.
// Шести хватает, чтобы отличить рост от случайного всплеска.
const SignupHistory = 6

// SignupDay — регистрации за одни сутки.
type SignupDay struct {
	Day   time.Time
	Count int
}

// SignupPeriod — регистрации за отрезок и изменение к предыдущему.
type SignupPeriod struct {
	From, To time.Time // To — последний день отрезка, включительно
	Count    int
	Change   int
	// ChangePct есть только при HasPct: когда в предыдущем отрезке не было
	// никого, рост «с нуля» в процентах не выражается.
	ChangePct int
	HasPct    bool
}

// SignupStats — всё, что показывает страница регистраций.
type SignupStats struct {
	Days     int
	Current  SignupPeriod
	Previous SignupPeriod
	Daily    []SignupDay // дни текущего отрезка, старые слева
	MaxDaily int
	// Periods — текущий отрезок и прошлые, свежий сверху.
	Periods   []SignupPeriod
	MaxPeriod int
	Total     int
}

// CountSignups раскладывает даты регистраций по суткам и отрезкам.
// Отрезки считаются календарными днями в поясе loc и заканчиваются
// сегодняшним днём: текущий отрезок включает неполные сегодняшние сутки.
func CountSignups(at []time.Time, now time.Time, days int, loc *time.Location) SignupStats {
	today := dayStart(now, loc)
	byDay := map[time.Time]int{}
	for _, t := range at {
		byDay[dayStart(t, loc)]++
	}

	st := SignupStats{Days: days, Total: len(at)}
	for i := days - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i)
		n := byDay[d]
		st.Daily = append(st.Daily, SignupDay{Day: d, Count: n})
		st.MaxDaily = max(st.MaxDaily, n)
	}

	// На один отрезок больше, чем показываем: самому старому из показанных
	// тоже нужно, с чем сравниться.
	counts := make([]SignupPeriod, SignupHistory+1)
	for k := range counts {
		to := today.AddDate(0, 0, -k*days)
		from := to.AddDate(0, 0, -(days - 1))
		p := SignupPeriod{From: from, To: to}
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			p.Count += byDay[d]
		}
		counts[k] = p
	}
	for k := 0; k < SignupHistory; k++ {
		p, prev := counts[k], counts[k+1]
		p.Change = p.Count - prev.Count
		if prev.Count > 0 {
			p.ChangePct = p.Change * 100 / prev.Count
			p.HasPct = true
		}
		st.Periods = append(st.Periods, p)
		st.MaxPeriod = max(st.MaxPeriod, p.Count)
	}
	st.Current, st.Previous = st.Periods[0], st.Periods[1]
	return st
}

// dayStart — полночь того дня, в который попадает t в поясе loc.
// Через Date, а не Truncate: Truncate режет по UTC и сдвинул бы границу
// суток на смещение пояса.
func dayStart(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
