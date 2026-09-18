package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Раскраска карты в самой игре — все режимы карты партии из админки
// разом. Состав партии присылает расширение: оно берёт его из клиента игры,
// открытого у человека. Сервер в партию не ходит и проверок карты
// не тратит — он только сводит присланное с базой и считает цвета теми же
// функциями, что и страница партии. Поэтому цвета и легенды в игре и
// в админке одни и те же и разойтись не могут.

// apiMapBodyLimit — предел тела запроса карты. На пятьсот игроков с их
// признаками и коалициями — около пятидесяти килобайт; запас двукратный
// с лишним, а мусорный запрос дальше этого не прочтётся.
const apiMapBodyLimit = 256 << 10

// apiMapMaxProvinces — больше провинций у одного игрока не бывает: на самых
// больших картах их всего около пяти тысяч.
const apiMapMaxProvinces = 20000

// Цвета режимов, которые в админке заданы в app.css классами, а не
// приезжают с данными. Держать их надо равными тем, что в стилях: тест
// сверяет. Серый — «про этот режим о владельце ничего не известно».
const (
	mapGrey        = "#4a5b68"
	mapSideFriend  = "#3f7d5a"
	mapSideEnemy   = "#a2453c"
	mapHuman       = "#3c6e9c"
	mapAI          = "#6f7b52"
	mapPremium     = "#7d63a8"
	mapBanned      = "#a2453c"
	mapPowerMuchUp = "#8f2f26"
	mapPowerUp     = "#c0564a"
	mapPowerEven   = "#b08a2e"
	mapPowerDown   = "#4f8f5f"
	mapPowerMuchDn = "#2f6b45"
)

// powerBands — полосы режима «Сила» в порядке легенды.
var powerBands = []struct{ class, key, color string }{
	{"power-much-up", "game.map.power.muchup", mapPowerMuchUp},
	{"power-up", "game.map.power.up", mapPowerUp},
	{"power-even", "game.map.power.even", mapPowerEven},
	{"power-down", "game.map.power.down", mapPowerDown},
	{"power-much-down", "game.map.power.muchdown", mapPowerMuchDn},
}

type apiMapPlayer struct {
	ID        int    `json:"id"`   // номер в партии
	Site      string `json:"site"` // номер на сайте, у ботов пусто
	AI        bool   `json:"ai"`
	Premium   bool   `json:"premium"`
	Banned    bool   `json:"banned"`
	Team      int    `json:"team"`
	Provinces int    `json:"provinces"`
}

type apiMapTeam struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"` // как в клиенте: «rgba(r,g,b,255)»
}

type apiMapRequest struct {
	Me      string         `json:"me"`
	Players []apiMapPlayer `json:"players"`
	Teams   []apiMapTeam   `json:"teams"`
}

// apiLegend — строка легенды. Count — число рядом с подписью: у альянсов
// провинции, у остальных — игроки, как и в легендах админки.
type apiLegend struct {
	Color string `json:"color"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// apiMode — один режим карты. Colors — цвет по номеру игрока в партии, уже
// в том виде, в каком его ждёт клиент игры; игроки без цвета в режиме
// получают серый. Note — пояснение к режиму, Status — строка над легендой:
// почему карта серая или с кем идёт сравнение.
type apiMode struct {
	Key    string         `json:"key"`
	Title  string         `json:"title"`
	Colors map[int]string `json:"colors"`
	Legend []apiLegend    `json:"legend"`
	Note   string         `json:"note"`
	Status string         `json:"status,omitempty"`
}

// apiMap — цвета всех режимов карты для партии, открытой у человека в игре.
func (s *Server) apiMap(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	var req apiMapRequest
	if !readJSONLimit(w, r, &req, apiMapBodyLimit) {
		return
	}
	state, ok := mapState(req)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, lang.T("api.map.bad"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiMapTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	sides, err := s.gameLists(r, state)
	if err != nil {
		s.apiMapFailed(w, r, err)
		return
	}
	// Незнакомых ставим в очередь воркеру, как и страница партии: в игре
	// человек сидит долго, и к следующему перечитыванию альянсы приедут.
	alliances, pending, err := s.gameAlliances(ctx, state, nil, true)
	if err != nil {
		s.apiMapFailed(w, r, err)
		return
	}
	sides.Alliances = alliances
	// Сравнивают с тем, кем человек играет в этой вкладке, а не с игровым
	// ID из профиля админки: аккаунтов в игре у него бывает несколько.
	sides.Stats, sides.Me, err = s.gameStats(ctx, state, req.Me)
	if err != nil {
		s.apiMapFailed(w, r, err)
		return
	}
	top, err := s.topAlliances.All(ctx)
	if err != nil {
		s.apiMapFailed(w, r, err)
		return
	}
	sides.Top = make(map[string]domain.TopAlliance, len(top))
	for _, t := range top {
		sides.Top[t.ID] = t
	}

	meID := 0
	if p, ok := viewerPlayer(state, req.Me); ok {
		meID = p.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"me":    meID,
		"modes": mapModes(lang, state, sides, meID, pending, !topCapturedAt(top).IsZero()),
	})
}

func (s *Server) apiMapFailed(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("карта для расширения", "err", err)
	writeAPIError(w, http.StatusInternalServerError, langOf(r).T("api.internal"))
}

// mapState собирает из присланного то состояние партии, которое понимают
// функции раскраски админки. Провинций у расширения поимённо нет — только
// сколько у кого, — а раскраске больше и не нужно: владения она лишь
// считает. Номера на сайте — только цифры, прочее к базе не пускаем.
func mapState(req apiMapRequest) (*supremacy.GameState, bool) {
	if !digitsOnly(req.Me) || len(req.Players) == 0 || len(req.Players) > apiMapMax ||
		len(req.Teams) > apiMapMax {
		return nil, false
	}
	state := &supremacy.GameState{
		Players: make(map[int]supremacy.Player, len(req.Players)),
		Teams:   make(map[int]supremacy.Team, len(req.Teams)),
	}
	for _, p := range req.Players {
		if p.ID <= 0 || p.Provinces < 0 || p.Provinces > apiMapMaxProvinces ||
			(p.Site != "" && !digitsOnly(p.Site)) {
			return nil, false
		}
		if _, dup := state.Players[p.ID]; dup {
			return nil, false
		}
		state.Players[p.ID] = supremacy.Player{
			ID: p.ID, SiteUserID: p.Site, TeamID: p.Team,
			Premium: p.Premium, Banned: p.Banned, IsAI: p.AI || p.Site == "",
		}
		for range p.Provinces {
			state.Provinces = append(state.Provinces, supremacy.Province{Owner: p.ID})
		}
	}
	for _, t := range req.Teams {
		if t.ID <= 0 || len([]rune(t.Name)) > 100 {
			return nil, false
		}
		state.Teams[t.ID] = supremacy.Team{ID: t.ID, Name: t.Name, Color: supremacy.CSSColor(t.Color)}
	}
	return state, true
}

// mapModes — все шесть режимов в том порядке, в каком они стоят над картой
// в админке.
func mapModes(l i18n.Lang, state *supremacy.GameState, sides *gameSides,
	meID, pending int, topKnown bool) []apiMode {

	// Сколько провинций у кого: легенды «Игроков» и «Силы» считают только
	// тех, у кого есть земля, — как и в админке.
	holders := map[int]bool{}
	for _, p := range state.Provinces {
		holders[p.Owner] = true
	}
	grey := func() map[int]string {
		out := make(map[int]string, len(state.Players))
		for id := range state.Players {
			out[id] = gameColor(mapGrey)
		}
		return out
	}

	// Альянсы.
	clans := apiMode{Key: "clans", Title: l.T("game.map.clans"), Colors: grey(),
		Note: l.T("game.map.clans.note")}
	fills, legend := allianceFills(l, state, sides.Alliances)
	for id, c := range fills {
		clans.Colors[id] = gameColor(c)
	}
	for _, a := range legend {
		label := a.Name
		if a.Tag != "" {
			label += " [" + a.Tag + "]"
		}
		clans.Legend = append(clans.Legend, apiLegend{Color: gameColor(nonEmpty(a.Color, mapGrey)),
			Label: label, Count: a.Provinces})
	}
	if len(legend) == 0 {
		clans.Status = l.T("game.map.clans.empty")
	}
	if pending > 0 {
		clans.Status = strings.TrimSpace(clans.Status + " " + l.T("game.map.clans.pending", pending))
	}

	// Мои списки. Вражда важнее дружбы — как и правила в app.css.
	lists := apiMode{Key: "sides", Title: l.T("game.map.mine"), Colors: grey(),
		Note: l.T("game.map.sides.note")}
	for id := range sides.Friends {
		lists.Colors[id] = gameColor(mapSideFriend)
	}
	for id := range sides.Enemies {
		lists.Colors[id] = gameColor(mapSideEnemy)
	}
	lists.Legend = []apiLegend{
		{Color: gameColor(mapSideEnemy), Label: l.T("game.map.sides.enemies"), Count: len(sides.Enemies)},
		{Color: gameColor(mapSideFriend), Label: l.T("game.map.sides.friends"), Count: len(sides.Friends)},
	}

	// Коалиции — цвета из самой игры.
	teams := apiMode{Key: "teams", Title: l.T("game.map.teams"), Colors: grey(),
		Note: l.T("game.map.teams.note")}
	teamOf, teamLegend := teamFills(l, state, meID)
	for id, c := range teamOf {
		teams.Colors[id] = gameColor(c)
	}
	for _, t := range teamLegend {
		label := t.Name
		if t.Mine {
			label += " (" + l.T("game.map.teams.yours") + ")"
		}
		teams.Legend = append(teams.Legend, apiLegend{Color: gameColor(t.Color), Label: label, Count: t.Members})
	}
	if len(teamLegend) == 0 {
		teams.Status = l.T("game.map.teams.empty")
	}

	// Игроки: бан важнее подписки, подписка — всего остального.
	kinds := apiMode{Key: "players", Title: l.T("game.map.players"), Colors: grey(),
		Note: l.T("game.map.players.note")}
	kindRows := []struct {
		key, color string
		n          int
	}{
		{"game.map.players.regular", mapHuman, 0},
		{"game.map.players.ai", mapAI, 0},
		{"game.map.premium.legend", mapPremium, 0},
		{"game.map.players.banned", mapBanned, 0},
	}
	for id, p := range state.Players {
		row := 0
		switch {
		case p.Banned:
			row = 3
		case p.Premium:
			row = 2
		case p.IsAI:
			row = 1
		}
		kinds.Colors[id] = gameColor(kindRows[row].color)
		if holders[id] {
			kindRows[row].n++
		}
	}
	for _, k := range kindRows {
		kinds.Legend = append(kinds.Legend, apiLegend{Color: gameColor(k.color), Label: l.T(k.key), Count: k.n})
	}

	// Сила — относительно того, кем человек играет в этой вкладке. Боты
	// и те, про кого сайт промолчал, остаются серыми.
	power := apiMode{Key: "power", Title: l.T("game.map.power"), Colors: grey(),
		Note: l.T("game.map.power.note")}
	counts := map[string]int{}
	unknown := 0
	for id, p := range state.Players {
		if p.IsAI {
			continue
		}
		cls := powerClass(sides.Stats[id], sides.Me)
		for _, b := range powerBands {
			if b.class == cls {
				power.Colors[id] = gameColor(b.color)
			}
		}
		if !holders[id] {
			continue
		}
		if cls == "" {
			unknown++
		} else {
			counts[cls]++
		}
	}
	for _, b := range powerBands {
		power.Legend = append(power.Legend, apiLegend{Color: gameColor(b.color), Label: l.T(b.key), Count: counts[b.class]})
	}
	power.Legend = append(power.Legend, apiLegend{Color: gameColor(mapGrey), Label: l.T("game.map.power.unknown"), Count: unknown})
	if me := sides.Me; me.Rated() {
		power.Status = l.T("game.map.power.you", me.Level, formatRatio(me.KD()), formatRatio(me.Danger()))
	} else {
		power.Status = l.T("game.map.power.nostats")
	}

	// Топ альянсов — цвет по месту в рейтинге.
	top := apiMode{Key: "top", Title: l.T("game.map.top"), Colors: grey(),
		Note: l.T("game.map.top.note")}
	topOf, topLegend := topFills(l, state, sides.Alliances, sides.Top)
	for id, row := range topOf {
		if row.Color != "" {
			top.Colors[id] = gameColor(row.Color)
		}
	}
	for _, row := range topLegend {
		label := fmt.Sprintf("%d. %s", row.Rank, row.Name)
		if row.Tag != "" {
			label += " [" + row.Tag + "]"
		}
		top.Legend = append(top.Legend, apiLegend{Color: gameColor(nonEmpty(row.Color, mapGrey)),
			Label: label, Count: row.Players})
	}
	switch {
	case !topKnown:
		top.Status = l.T("game.map.top.none")
	case len(topLegend) == 0:
		top.Status = l.T("game.map.top.empty")
	}

	modes := []apiMode{clans, lists, teams, kinds, power, top}
	for i := range modes {
		// Пустые строки легенды не показываем: «Забаненные — 0» только шумит.
		kept := modes[i].Legend[:0]
		for _, row := range modes[i].Legend {
			if row.Count > 0 {
				kept = append(kept, row)
			}
		}
		modes[i].Legend = kept
		if modes[i].Legend == nil {
			modes[i].Legend = []apiLegend{}
		}
	}
	return modes
}

// gameColor переводит цвет из css — «#rrggbb» или «rgb(r,g,b)» — в тот вид,
// в каком цвета страны держит клиент игры: «rgba(r,g,b,255)». Другой вид
// клиент не разберёт. Браузер этот же цвет понимает и в легенде: альфу
// больше единицы он просто считает единицей.
func gameColor(css string) string {
	var r, g, b int
	switch {
	case len(css) == 7 && css[0] == '#':
		v, err := strconv.ParseUint(css[1:], 16, 32)
		if err != nil {
			return gameColor(mapGrey)
		}
		r, g, b = int(v>>16), int(v>>8&0xff), int(v&0xff)
	case strings.HasPrefix(css, "rgb(") && strings.HasSuffix(css, ")"):
		parts := strings.Split(css[4:len(css)-1], ",")
		if len(parts) != 3 {
			return gameColor(mapGrey)
		}
		nums := make([]int, 3)
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return gameColor(mapGrey)
			}
			nums[i] = n
		}
		r, g, b = nums[0], nums[1], nums[2]
	default:
		return gameColor(mapGrey)
	}
	return fmt.Sprintf("rgba(%d,%d,%d,255)", r, g, b)
}
