package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Потери в войнах по газете партии — вкладка карты «Потери». Считаются
// только по нажатию: газета отдаётся по дню за запрос, и на долгой партии
// это десятки запросов к игре. Прошедшие дни игровой клиент держит
// в памяти, так что второй подсчёт быстрый.

// lossesBands — пять ступеней заливки от «потерял мало» к «потерял много»,
// считаются от самого пострадавшего в партии.
var lossesBands = []string{"#e8c9a8", "#d99a6c", "#c46a45", "#a03e2c", "#6e1f1a"}

type lossesView struct {
	Rows    []lossesRow
	Days    int
	Missing string
	Why     string
	// Style — правила заливки провинций по владельцу. Карта уже на
	// странице, перерисовывать её ради цветов незачем: ответ приносит
	// только css к ней.
	Style template.CSS
}

type lossesRow struct {
	Nation    string
	Name      string
	Mine      bool
	OnMap     bool
	Lost      string
	Killed    string
	Ratio     string
	Color     string
	Opponents []lossesDuel
}

type lossesDuel struct {
	Nation string
	Lost   string
	Killed string
}

// gameLosses отдаёт блок потерь для вкладки карты. Пускает того же, кого
// пускает дорисовка состава: кто только что видел карту этой партии.
func (s *Server) gameLosses(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	who := currentUser(r)
	lang := langOf(r)

	fail := func(msg string) {
		s.renderPartial(w, r, "game", "gamelosses", map[string]any{"Error": msg, "GameID": gameID})
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
	losses, err := s.games.GameLosses(ctx, gameID, state.Day)
	if err != nil {
		s.log.Warn("потери по газете", "gameID", gameID, "err", err)
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
	s.renderPartial(w, r, "game", "gamelosses", map[string]any{
		"GameID": gameID, "Losses": lossesData(lang, state, losses, meID),
	})
}

// lossesData готовит таблицу и заливку. Имена — из состояния партии, если
// игрок там есть; иначе как его назвала газета.
func lossesData(l i18n.Lang, state *supremacy.GameState, losses *supremacy.Losses, meID int) *lossesView {
	v := &lossesView{Days: losses.Days, Why: losses.Why}
	if len(losses.Missing) > 0 {
		days := make([]string, len(losses.Missing))
		for i, d := range losses.Missing {
			days[i] = strconv.Itoa(d)
		}
		v.Missing = strings.Join(days, ", ")
	}

	onMap := map[int]bool{}
	for _, p := range state.Provinces {
		if p.Owner > 0 {
			onMap[p.Owner] = true
		}
	}
	nation := func(id int, fallback string) (string, string) {
		if p, ok := state.Players[id]; ok {
			return nonEmpty(p.Nation, fallback), p.Name
		}
		return nonEmpty(fallback, fmt.Sprintf("#%d", id)), ""
	}

	var most int64
	for _, p := range losses.Players {
		most = max(most, p.Lost)
	}

	var style strings.Builder
	for _, p := range losses.Players {
		n, name := nation(p.ID, p.Name)
		row := lossesRow{
			Nation: n, Name: name, Mine: meID > 0 && p.ID == meID, OnMap: onMap[p.ID],
			Lost: groupDigits(p.Lost), Killed: groupDigits(p.Killed), Ratio: formatKD(p.Killed, p.Lost),
		}
		if most > 0 && p.Lost > 0 {
			band := int(float64(p.Lost) / float64(most) * float64(len(lossesBands)))
			row.Color = lossesBands[min(band, len(lossesBands)-1)]
			fmt.Fprintf(&style, "#map-losses:checked ~ .game-map polygon[data-owner=\"%d\"] { fill: %s; }\n",
				p.ID, row.Color)
		}
		for _, d := range p.Opponent {
			on, _ := nation(d.OpponentID, d.OpponentName)
			row.Opponents = append(row.Opponents, lossesDuel{
				Nation: on, Lost: groupDigits(d.Lost), Killed: groupDigits(d.Killed)})
		}
		v.Rows = append(v.Rows, row)
	}
	v.Style = template.CSS(style.String())
	return v
}

// formatKD — сколько убил на одного потерянного.
func formatKD(killed, lost int64) string {
	if lost == 0 {
		if killed == 0 {
			return "—"
		}
		return "∞"
	}
	return strconv.FormatFloat(float64(killed)/float64(lost), 'f', 2, 64)
}

// groupDigits — 1234567 → «1 234 567».
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
