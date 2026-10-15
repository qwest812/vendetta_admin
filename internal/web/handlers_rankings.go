package web

// Вкладки карты «Ресурсы» и «Устрашение»: рейтинги из газеты —
// крупнейшие экономики (производство ресурсов в тоннах за сутки)
// и самые устрашающие страны. Газета печатает каждый раз в несколько
// дней; карта красится по последнему такому дню, а под ней — график
// по всем: кто рос, кто проседал.

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Рейтинги — двух видов, у каждого своя вкладка. Значения — те же, что
// в адресе вкладки (?kind=…).
const (
	rankRes  = "res"
	rankFear = "fear"
)

// Заливка карты — одна шкала от светлого к тёмному на вид рейтинга:
// темнее — больше производит или страшнее. Зелёная у ресурсов и красная
// у устрашения, чтобы вкладки не путались между собой и с «Потерями».
var (
	resBands  = []string{"#cfe8c4", "#9ccf8a", "#5fae55", "#2f8a3a", "#14602a"}
	fearBands = []string{"#f0c9c9", "#e09a9a", "#cc6464", "#a83a3a", "#741f1f"}
)

// rankSeriesMax — сколько стран на графике. Цветов в палитре восемь,
// девятая слилась бы с кем-то; остальные — в таблице под графиком.
const rankSeriesMax = 8

type rankRow struct {
	Place  int
	Nation string
	// Name — ник владельца, если он есть в партии.
	Name  string
	Value string
	Color string
	Mine  bool
	// Series — номер линии на графике, 1…8; ноль — на графике нет.
	Series int
}

type rankPoint struct {
	X, Y float64
	Tip  string
}

type rankLine struct {
	Series int
	Label  string
	// Paths — куски линии: страна выпадала из рейтинга, и между днями
	// без неё линия рвётся, а не врёт прямой.
	Paths []string
	Dots  []rankPoint
	// End — где подписать линию: правее последней точки.
	End rankPoint
}

type rankTick struct {
	Pos   float64
	Label string
}

// rankChart — график по дням: по горизонтали дни, по вертикали доля
// богатства или место в рейтинге (первое сверху).
type rankChart struct {
	W, H         float64
	Left, Bottom float64
	Top, Right   float64
	// GridEnd, TickX, TickY — посчитанные концы сетки и места подписей
	// осей: шаблоны не считают.
	GridEnd, TickX, TickY float64
	Lines                 []rankLine
	XTicks                []rankTick
	YTicks                []rankTick
	// Table — те же данные таблицей: страна × день.
	Days  []int
	Table []rankTableRow
}

type rankTableRow struct {
	Label  string
	Series int
	Cells  []string
}

type rankingsView struct {
	Kind    string
	Days    int
	Day     int
	Missing string
	Why     string
	At      time.Time
	Rows    []rankRow
	Style   template.CSS
	Chart   *rankChart
}

// gameRankings отдаёт блок вкладки «Ресурсы» или «Устрашение». Пускает
// того же, кого пускают «Потери», — кто недавно видел карту этой партии.
func (s *Server) gameRankings(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	kind := r.URL.Query().Get("kind")
	if kind != rankFear {
		kind = rankRes
	}
	who := currentUser(r)
	lang := langOf(r)
	fail := func(msg string) {
		s.renderPartial(w, r, "game", "gamerankings", map[string]any{"Error": msg, "GameID": gameID, "Kind": kind})
	}
	if s.games == nil || !s.mapTickets.has(who.ID, gameID, lossesTicketTTL) {
		fail(lang.T("game.losses.expired"))
		return
	}
	state, ok := s.games.CachedState(gameID)
	if !ok {
		fail(lang.T("game.losses.expired"))
		return
	}
	if state.Anonymous && !who.CanSeeAnonymousMaps() {
		fail(lang.T("game.anonymous.closed"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), bigGameTimeout)
	defer cancel()
	ranks, err := s.games.GameRankings(ctx, gameID, state.Day)
	if err != nil {
		s.log.Warn("рейтинги по газете", "gameID", gameID, "err", err)
		msg := lang.T("game.losses.error", err)
		if errors.Is(err, context.DeadlineExceeded) {
			msg = lang.T("game.losses.slow")
		}
		fail(msg)
		return
	}

	meID := 0
	if me, playing := viewerPlayer(state, who.GameID); playing {
		meID = me.ID
	}
	s.renderPartial(w, r, "game", "gamerankings", map[string]any{
		"GameID": gameID, "Kind": kind, "Ranks": rankingsData(lang, kind, state, ranks, meID),
	})
}

// rankingsData готовит легенду, заливку и график одного вида рейтинга.
func rankingsData(l i18n.Lang, kind string, state *supremacy.GameState, ranks *supremacy.Rankings, meID int) *rankingsView {
	v := &rankingsView{Kind: kind, At: time.Now(), Why: ranks.Why}
	if len(ranks.Missing) > 0 {
		days := make([]string, len(ranks.Missing))
		for i, d := range ranks.Missing {
			days[i] = strconv.Itoa(d)
		}
		v.Missing = strings.Join(days, ", ")
	}

	// Строки по дням. Значение — тонны у ресурсов, место у устрашения.
	type entry struct {
		owner int
		label string
		value float64
	}
	byDay := map[int][]entry{}
	var days []int
	for _, d := range ranks.Days {
		lines := d.Economy
		if kind == rankFear {
			lines = d.Fear
		}
		var row []entry
		for i, r := range lines {
			value := float64(r.Value)
			if kind == rankFear {
				value = float64(i + 1)
			}
			row = append(row, entry{owner: r.PlayerID, label: r.Nation, value: value})
		}
		if len(row) > 0 {
			byDay[d.Day] = row
			days = append(days, d.Day)
		}
	}
	v.Days = len(days)
	if len(days) == 0 {
		return v
	}
	last := days[len(days)-1]
	v.Day = last

	player := func(owner int, fallback string) (string, string) {
		if p, ok := state.Players[owner]; ok {
			return nonEmpty(p.Nation, fallback), p.Name
		}
		return fallback, ""
	}

	// Линии — у тех, кто в последнем дне, по порядку рейтинга, и если
	// мест осталось — у выпавших раньше, по лучшему их значению. Цвет
	// закреплён за страной на весь график.
	series := map[int]int{}
	labels := map[int]string{}
	for _, e := range byDay[last] {
		if len(series) < rankSeriesMax {
			series[e.owner] = len(series) + 1
		}
	}
	best := map[int]float64{}
	var others []int
	for _, d := range days {
		for _, e := range byDay[d] {
			labels[e.owner], _ = player(e.owner, e.label)
			if _, ok := series[e.owner]; ok {
				continue
			}
			if b, seen := best[e.owner]; !seen {
				others = append(others, e.owner)
				best[e.owner] = e.value
			} else if betterRank(kind, e.value, b) {
				best[e.owner] = e.value
			}
		}
	}
	sort.SliceStable(others, func(i, j int) bool { return betterRank(kind, best[others[i]], best[others[j]]) })
	for _, o := range others {
		if len(series) >= rankSeriesMax {
			break
		}
		series[o] = len(series) + 1
	}

	// Легенда и заливка — по последнему дню.
	var style strings.Builder
	mode := map[string]string{rankRes: "map-res", rankFear: "map-fear"}[kind]
	today := byDay[last]
	top := 0.0
	for _, e := range today {
		top = math.Max(top, e.value)
	}
	for i, e := range today {
		nation, name := player(e.owner, e.label)
		row := rankRow{Place: i + 1, Nation: nation, Name: name,
			Mine: meID > 0 && e.owner == meID, Series: series[e.owner]}
		if kind == rankRes {
			row.Value = rankValue(l, kind, e.value)
			band := 0
			if top > 0 {
				band = int(e.value / top * float64(len(resBands)))
			}
			row.Color = resBands[min(band, len(resBands)-1)]
		} else {
			row.Value = rankValue(l, kind, e.value)
			band := len(fearBands) - 1 - i*len(fearBands)/max(len(today), 1)
			row.Color = fearBands[max(band, 0)]
		}
		fmt.Fprintf(&style, "#%s:checked ~ .game-map polygon[data-owner=\"%d\"] { fill: %s; }\n",
			mode, e.owner, row.Color)
		v.Rows = append(v.Rows, row)
	}
	v.Style = template.CSS(style.String())
	v.Chart = rankChartFor(l, kind, days, func(d int) map[int]float64 {
		out := map[int]float64{}
		for _, e := range byDay[d] {
			out[e.owner] = e.value
		}
		return out
	}, series, labels)
	return v
}

// betterRank — значение a лучше b: у ресурсов — больше тонн, у
// устрашения — выше место (меньше число).
func betterRank(kind string, a, b float64) bool {
	if kind == rankRes {
		return a > b
	}
	return a < b
}

// rankChartFor рисует график по дням. Значения — по дням и ключам,
// линии — только у тех, кому выдан номер в series.
func rankChartFor(l i18n.Lang, kind string, days []int, valuesOn func(int) map[int]float64,
	series map[int]int, labels map[int]string) *rankChart {

	// Слева — подписи шкалы: «200 тыс. т» у ресурсов, номер места у
	// устрашения; отступ под самую длинную из них.
	left := 28.0
	if kind == rankRes {
		left = 76
	}
	c := &rankChart{W: 720, H: 260, Left: left, Bottom: 28, Top: 12, Right: 120, Days: days}
	c.GridEnd, c.TickX, c.TickY = c.W-c.Right, c.Left-6, c.H-8
	plotW, plotH := c.W-c.Left-c.Right, c.H-c.Top-c.Bottom
	first, lastDay := days[0], days[len(days)-1]

	// Вертикаль: доля — от нуля до круглой верхушки; место — с первого
	// сверху до последнего занятого внизу.
	lo, hi := 0.0, 1.0
	values := make(map[int]map[int]float64, len(days))
	for _, d := range days {
		values[d] = valuesOn(d)
		for _, v := range values[d] {
			hi = math.Max(hi, v)
		}
	}
	if kind == rankRes {
		hi = niceCeil(hi)
	} else {
		lo = 1
	}
	x := func(d int) float64 {
		if lastDay == first {
			return c.Left + plotW/2
		}
		return c.Left + plotW*float64(d-first)/float64(lastDay-first)
	}
	y := func(v float64) float64 {
		if hi == lo {
			return c.Top + plotH/2
		}
		f := (v - lo) / (hi - lo)
		if kind == rankFear {
			return c.Top + plotH*f // первое место — сверху
		}
		return c.Top + plotH*(1-f)
	}

	step := max(1, (lastDay-first+1)/10)
	for d := first; d <= lastDay; d += step {
		c.XTicks = append(c.XTicks, rankTick{Pos: x(d), Label: strconv.Itoa(d)})
	}
	if kind == rankRes {
		for v := 0.0; v <= hi; v += hi / 4 {
			c.YTicks = append(c.YTicks, rankTick{Pos: y(v), Label: l.T("game.rank.kt", groupDigits(int64(v/1000)))})
		}
	} else {
		for v := 1.0; v <= hi; v++ {
			c.YTicks = append(c.YTicks, rankTick{Pos: y(v), Label: strconv.Itoa(int(v))})
		}
	}

	keys := make([]int, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return series[keys[i]] < series[keys[j]] })
	for _, k := range keys {
		line := rankLine{Series: series[k], Label: labels[k]}
		var path strings.Builder
		for _, d := range days {
			v, ok := values[d][k]
			if !ok {
				if path.Len() > 0 {
					line.Paths = append(line.Paths, path.String())
					path.Reset()
				}
				continue
			}
			px, py := x(d), y(v)
			if path.Len() == 0 {
				fmt.Fprintf(&path, "M%.1f %.1f", px, py)
			} else {
				fmt.Fprintf(&path, " L%.1f %.1f", px, py)
			}
			tip := l.T("game.rank.tip", labels[k], d, rankValue(l, kind, v))
			line.Dots = append(line.Dots, rankPoint{X: px, Y: py, Tip: tip})
			line.End = rankPoint{X: px + 8, Y: py}
		}
		if path.Len() > 0 {
			line.Paths = append(line.Paths, path.String())
		}
		c.Lines = append(c.Lines, line)

		row := rankTableRow{Label: labels[k], Series: series[k]}
		for _, d := range days {
			if v, ok := values[d][k]; ok {
				row.Cells = append(row.Cells, rankValue(l, kind, v))
			} else {
				row.Cells = append(row.Cells, "—")
			}
		}
		c.Table = append(c.Table, row)
	}
	spreadLabels(c.Lines, rankLabelGap, c.Top, c.H-c.Bottom)
	return c
}

// rankLabelGap — сколько места по высоте нужно подписи линии.
const rankLabelGap = 13

// spreadLabels разводит подписи линий по высоте, чтобы не наезжали:
// линии, кончающиеся рядом, дают подписи друг над другом. Подписи
// кончающихся раньше стоят в другом месте по горизонтали и друг другу
// не мешают, поэтому разводятся только те, что у одного края.
func spreadLabels(lines []rankLine, gap, top, bottom float64) {
	byX := map[float64][]int{}
	for i, ln := range lines {
		if len(ln.Dots) > 0 {
			byX[ln.End.X] = append(byX[ln.End.X], i)
		}
	}
	for _, idx := range byX {
		sort.Slice(idx, func(a, b int) bool { return lines[idx[a]].End.Y < lines[idx[b]].End.Y })
		prev := math.Inf(-1)
		for _, i := range idx {
			y := math.Max(lines[i].End.Y, prev+gap)
			lines[i].End.Y, prev = y, y
		}
		// Ушли за низ — сдвигаем всю стопку вверх, сколько позволяет верх.
		if over := prev - bottom; over > 0 {
			for _, i := range idx {
				lines[i].End.Y = math.Max(top, lines[i].End.Y-over)
			}
		}
	}
}

func rankValue(l i18n.Lang, kind string, v float64) string {
	if kind == rankRes {
		return l.T("game.rank.tons", groupDigits(int64(v)))
	}
	return l.T("game.rank.place", int(v))
}

// niceCeil — круглая верхушка шкалы: 42 402 → 50 000, 8 200 → 10 000.
// Делится на четыре ровные доли без дробей.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 4
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 4, 5, 8, 10} {
		if v <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}
