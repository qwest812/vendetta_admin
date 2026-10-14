package web

import (
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Границы режима «Сила»: во сколько раз владелец провинции опаснее
// смотрящего. Опасность — уровень, умноженный на кд: уровень один говорит
// лишь о наигранном времени, кд одно — об умении, опасен тот, у кого сошлось
// и то и другое.
//
// Полосы намеренно широкие в середине: разница в десяток процентов — это
// не «сильнее», а «примерно поровну», и красить её в тревожный цвет значит
// врать.
const (
	powerMuchUp = 1.5
	powerUp     = 1.15
	powerEven   = 0.85
	powerDown   = 0.5
)

// mapShape — одна провинция на рисунке карты. Ни заливки, ни обводки здесь
// нет: карта рисуется один раз, а красится по-разному в зависимости от
// выбранного режима, поэтому провинция несёт признаки сразу всех режимов,
// а цвет из них выбирает css.
//
// Class — «neutral» у ничейной земли, «clan» у той, чей клан получил цвет,
// «top» у страны игрока из клана-топа, «side-enemy» и «side-friend» у стран
// из личных списков смотрящего, «team» у состоящей в коалиции, «human»
// и «ai» у страны живого игрока и бота, «premium» и «banned» у страны
// игрока с подпиской и забаненного, «power-…» у той, чей владелец сильнее
// или слабее смотрящего, «own» у своей: эта последняя пометка одна на все
// режимы, потому что обводка везде означает одно и то же.
// ClanColor, TeamColor и TopColor — цвета клана, коалиции и клана из топа,
// они же css-переменные --clan, --team и --top; пусто, если красить нечем.
type mapShape struct {
	Points string
	// Owner — номер владельца в партии; по нему вкладка «Потери»
	// красит провинции, не перерисовывая карту.
	Owner     int
	Class     string
	ClanColor template.CSS
	TeamColor template.CSS
	TopColor  template.CSS
	// TogetherColor — цвет группы в режиме «Играли вместе».
	TogetherColor template.CSS
	Title         string
}

// mapLabel — подпись страны на карте: имя пишется один раз, посередине
// её владений. Enemy и Friend — из личных списков смотрящего, и вместе они
// не встречаются: один и тот же игрок либо там, либо там.
//
// Mark — значок перед названием (меч или рукопожатие). Он хранится отдельно
// от имени, потому что красится по-своему: страница рисует его собственным
// tspan.
//
// Nick — ник игрока второй строкой под названием страны. Пусто у ботов
// и там, где ник уже стоит первой строкой: страна без названия
// подписывается именно им.
type mapLabel struct {
	X      int
	Y      int
	Mark   string
	Text   string
	Nick   string
	Enemy  bool
	Friend bool
}

// gameMapView — карта партии как её рисует страница: система координат
// у игры своя, поэтому размеры отдаются в шаблон и уходят в viewBox.
type gameMapView struct {
	Width  int
	Height int
	Shapes []mapShape
	Labels []mapLabel
	// Legend — какому альянсу какой цвет достался в этой партии. Без неё
	// заливка ничего не значит: цвет раздаётся на партию, а не закреплён
	// за альянсом навсегда.
	Legend []allianceLegendView
	// Teams — коалиции партии для своего режима карты. Цвет у них свой,
	// игровой, поэтому раздавать его почти не приходится.
	Teams []teamLegendView
	// Top — кланы из верхушки рейтинга игры, чьи игроки нашлись в этой
	// партии. Пусто и когда таких нет, и когда топ ещё не снимали.
	Top []topLegendView
	// Together — группы режима «Играли вместе».
	Together []togetherLegendView
	// Kinds — счёт владельцев для легенды режима «Игроки».
	Kinds playerKinds
	// Activity — счёт владельцев по полосам режима «Активность».
	Activity activityLegend
	// Power — счёт владельцев по полосам режима «Сила» и счёт самого
	// смотрящего: без него полосы не значат ничего.
	Power powerLegend
}

// powerLegend — сколько владельцев в каждой полосе режима «Сила» и с чем
// их сравнивали. Me — счёт смотрящего: легенда показывает его числом,
// иначе «опаснее» и «слабее» повисают в воздухе.
//
// Unknown — те, про кого счёта ещё нет: серый цвет здесь означает не
// «слабый», а «не спрашивали», и это стоит сказать отдельно.
type powerLegend struct {
	MuchUp   int
	Up       int
	Even     int
	Down     int
	MuchDown int
	Unknown  int
	Me       domain.UserStats
}

// playerKinds — сколько на карте владельцев каждого рода. Легенда режима
// «Игроки» только этим и живёт: цвет там говорит не про клан и не про
// коалицию, а про самого человека, и список имён к нему не нужен —
// достаточно чисел.
//
// Роды не пересекаются: каждый владелец попадает ровно в один, тот же,
// в какой его красит css.
// activityLegend — сколько владельцев в каждой полосе режима «Активность».
// Считаются люди, а не провинции; боты — отдельно, у них входов нет.
type activityLegend struct {
	Today    int
	Recent   int
	Old      int
	Inactive int
	Unknown  int
	AI       int
}

// Полосы режима «Активность» — по настоящему времени: сутки и трое суток.
const (
	activityToday  = 24 * time.Hour
	activityRecent = 72 * time.Hour
)

// activityClass — полоса владельца для режима «Активность»: класс
// провинции. Пусто у ботов и у тех, про чей вход игра не сказала.
func activityClass(p supremacy.Player, now time.Time) string {
	switch {
	case p.IsAI:
		return ""
	case p.Inactive:
		return "act-gone"
	case p.LastLogin.IsZero():
		return ""
	case now.Sub(p.LastLogin) < activityToday:
		return "act-today"
	case now.Sub(p.LastLogin) < activityRecent:
		return "act-recent"
	}
	return "act-old"
}

// agoText — «3 ч назад», «2 дн назад» для подсказки.
func agoText(l i18n.Lang, d time.Duration) string {
	switch {
	case d < time.Hour:
		return l.T("ago.minutes")
	case d < activityToday:
		return l.T("ago.hours", int(d.Hours()))
	}
	return l.T("ago.days", int(d.Hours()/24))
}

type playerKinds struct {
	Regular int
	AI      int
	Premium int
	Banned  int
}

// topLegendView — строка легенды режима «Топ кланов»: клан из верхушки
// рейтинга и его место там. Место важнее владений: в этом режиме смотрят
// не на то, кто больше занял, а на то, с кем имеешь дело.
type topLegendView struct {
	ID        string
	Rank      int
	Name      string
	Tag       string
	Color     string
	Players   int
	Provinces int
}

// teamLegendView — строка легенды режима «Коалиции»: название из игры,
// её же цвет и сколько игроков партии в ней состоит. Mine — коалиция
// смотрящего: своя должна узнаваться без пересчёта стран.
type teamLegendView struct {
	Name    string
	Color   string
	Members int
	Mine    bool
}

// allianceLegendView — строка легенды под картой: альянс из самой игры.
// Пустой Color означает альянс, которому цвета не хватило: он нарисован
// общим серым, но в легенде остаётся — иначе о нём на карте не узнать вовсе.
type allianceLegendView struct {
	ID        string
	Name      string
	Tag       string
	Color     string
	Provinces int
}

// mapPalette — цвета кланов. Приглушённые: поверх заливки должны читаться
// и названия стран, и жёлтая обводка своих провинций. Цвета раздаются по
// убыванию владений, поэтому крупнейший альянс партии получает первый цвет
// списка. Кланов в партии обычно единицы; тем, кому цвета не хватило,
// достаётся общий серый.
var mapPalette = []string{
	"#c4694f", // терракотовый
	"#5f8fb0", // синий
	"#7a9a5c", // зелёный
	"#a07bb8", // фиолетовый
	"#c9a44c", // охра
	"#4f9b91", // бирюзовый
	"#c07396", // розовый
	"#94795e", // коричневый
}

// gameMap готовит очертания карты сразу для всех режимов страницы: заливку
// по кланам из базы, по личным спискам смотрящего и по коалициям из самой
// партии. Что из этого показать, решает css — рисуется карта один раз. Собственные цвета стран из игры не используются: на них
// карта пестрит, а стороны конфликта всё равно не видны.
// Провинции, которых нет в геометрии, просто не рисуются: файл карты общий
// для всех партий и меняется отдельно от них.
// meID — номер смотрящего в этой партии, его провинции обводим отдельно.
// Ноль означает, что своей страны здесь нет: обводить нечего. Обводка только
// одна, своя: страны из личных списков и без неё видны по значку и цвету
// названия, а лишние рамки на карте только рябят.
func gameMap(l i18n.Lang, geo *supremacy.MapGeometry, state *supremacy.GameState,
	sides *gameSides, meID int) *gameMapView {

	if sides == nil {
		sides = &gameSides{}
	}
	fills, legend := allianceFills(l, state, sides.Alliances)
	teamOf, teams := teamFills(l, state, meID)
	topOf, topLegend := topFills(l, state, sides.Alliances, sides.Top)

	view := &gameMapView{
		Width: geo.Width, Height: geo.Height, Legend: legend, Teams: teams, Top: topLegend,
		Together: sides.TogetherLegend,
	}
	// Середина владений каждой страны — там и будет её имя.
	centers := map[int]*center{}
	now := time.Now()

	for _, p := range state.Provinces {
		points, ok := geo.Land[p.ID]
		if !ok {
			continue
		}

		shape := mapShape{Points: svgPoints(points), Title: p.Name, Owner: p.Owner}
		// Ничейная земля своим классом отличается от занятой: в обоих
		// режимах она серая, но темнее — свободную землю не должно быть
		// видно как чью-то.
		classes := []string{"neutral"}
		if p.Owner > 0 {
			classes = classes[:0]
			owner := state.Players[p.Owner]
			// Своего цвета у страны на нашей карте нет: в режиме кланов
			// серым остаётся и тот, кого нет в базе, и тот, кто в базе
			// без клана.
			if color, ok := fills[p.Owner]; ok {
				classes = append(classes, "clan")
				shape.ClanColor = template.CSS(color)
			}
			shape.Title = p.Name + " — " + nonEmpty(owner.Nation, l.T("game.unknown"))
			if owner.Name != "" {
				shape.Title += " (" + owner.Name + ")"
			}
			// Клан в подсказке нужен и покрашенным: по цвету название
			// не восстановить, а серые кланы на карте не отличить
			// от игроков без клана вовсе.
			if a, ok := sides.Alliances[p.Owner]; ok && a.InClan() {
				shape.Title += ", " + l.T("tip.clan", nonEmpty(a.Name, l.T("game.noname")))
			}
			// Клан из верхушки рейтинга — свой режим и своя пометка
			// в подсказке: цвет говорит, из какого именно клана игрок,
			// а место в рейтинге словами.
			if top, ok := topOf[p.Owner]; ok {
				// Класс — только вместе с цветом: как и в режиме кланов,
				// не влезший в палитру остаётся серым, но в подсказке
				// и в легенде он есть.
				if top.Color != "" {
					classes = append(classes, "top")
					shape.TopColor = template.CSS(top.Color)
				}
				shape.Title += ", " + l.T("tip.top", top.Rank,
					nonEmpty(top.Name, l.T("game.noname")))
			}
			// Активность: когда владелец последний раз заходил в партию.
			// Класс — для своего режима, время — в подсказку всегда.
			if cls := activityClass(owner, now); cls != "" {
				classes = append(classes, cls)
			}
			switch {
			case owner.Inactive:
				shape.Title += ", " + l.T("tip.inactive")
			case !owner.IsAI && !owner.LastLogin.IsZero():
				shape.Title += ", " + l.T("tip.lastlogin", agoText(l, now.Sub(owner.LastLogin)))
			}
			// Играли вместе: цвет группы в своём режиме, напарники —
			// в подсказке всегда.
			if mark, ok := sides.Together[p.Owner]; ok {
				if mark.Color != "" {
					classes = append(classes, "together")
					shape.TogetherColor = template.CSS(mark.Color)
				}
				shape.Title += ", " + l.T("tip.together", strings.Join(mark.Partners, ", "))
			}
			// Личные списки красят карту в своём режиме, а в подсказке
			// живут всегда: наводят как раз чтобы понять, чья провинция.
			if _, ok := sides.Friends[p.Owner]; ok {
				classes = append(classes, "side-friend")
				shape.Title += ", " + l.T("rel.friends.markdone")
			}
			if _, ok := sides.Enemies[p.Owner]; ok {
				// Вражда важнее дружбы: в спорном случае класс врага идёт
				// последним и по порядку правил в css побеждает.
				classes = append(classes, "side-enemy")
				shape.Title += ", " + l.T("rel.enemies.markdone")
			}
			// Коалиция — из самой игры, в отличие от клана: её игроки
			// объявили друг друга союзниками прямо в партии, и цвет
			// коалиция выбирает себе тоже сама.
			if color, ok := teamOf[p.Owner]; ok {
				classes = append(classes, "team")
				shape.TeamColor = template.CSS(color)
				shape.Title += ", " + l.T("tip.team", nonEmpty(state.Teams[owner.TeamID].Name, l.T("game.noname")))
			}
			// Режим «Игроки» различает четверых: живого человека, бота,
			// подписку и бан. Все четыре — свойства самого игрока, из
			// состояния партии, и класс нужен каждому: серой заливкой
			// по умолчанию помечена ничейная земля, и обычный игрок
			// не должен выглядеть как она.
			if owner.IsAI {
				classes = append(classes, "ai")
			} else {
				classes = append(classes, "human")
			}
			if owner.Premium {
				classes = append(classes, "premium")
				shape.Title += ", " + l.T("game.premium")
			}
			if owner.Banned {
				classes = append(classes, "banned")
				shape.Title += ", " + l.T("game.bannedone")
			}
			// Боевой счёт: класс — только для своего режима, а уровень
			// и кд в подсказке нужны всегда. По ним и понятно, почему
			// провинция такого цвета.
			if st, ok := sides.Stats[p.Owner]; ok && st.Rated() {
				if cls := powerClass(st, sides.Me); cls != "" {
					classes = append(classes, cls)
				}
				shape.Title += ", " + l.T("tip.power", st.Level, formatRatio(st.KD()))
			}

			c := centers[p.Owner]
			if c == nil {
				c = &center{}
				centers[p.Owner] = c
			}
			c.add(points)
		}
		// Свои провинции обводим: на политической карте иначе не найти,
		// чей цвет наш. Обводка на все режимы одна — режимы спорят
		// за заливку, а «где я» в каждом из них вопрос один и тот же.
		if meID > 0 && p.Owner == meID {
			classes = append(classes, "own")
		}
		shape.Class = strings.Join(classes, " ")
		view.Shapes = append(view.Shapes, shape)
	}

	// Легенда режима «Игроки» считает владельцев, а не провинции: цвет
	// говорит про человека, и в centers лежат ровно те, у кого земля есть.
	// Порядок веток тот же, в каком спорят правила css: бан важнее
	// подписки, подписка — всего остального.
	for playerID := range centers {
		owner := state.Players[playerID]
		switch activityClass(owner, now) {
		case "act-today":
			view.Activity.Today++
		case "act-recent":
			view.Activity.Recent++
		case "act-old":
			view.Activity.Old++
		case "act-gone":
			view.Activity.Inactive++
		default:
			if owner.IsAI {
				view.Activity.AI++
			} else {
				view.Activity.Unknown++
			}
		}

		switch {
		case owner.Banned:
			view.Kinds.Banned++
		case owner.Premium:
			view.Kinds.Premium++
		case owner.IsAI:
			view.Kinds.AI++
		default:
			view.Kinds.Regular++
		}

		// Легенда режима «Сила» считает тех же владельцев по своим полосам,
		// но без компьютерных: счёта у бота нет и не будет, и в строке
		// «счёта ещё нет» он читался бы как незаконченная работа.
		// Неизвестные считаются отдельно намеренно: серая провинция живого
		// игрока значит «сайт о нём промолчал», и молчать об этом нельзя.
		if owner.IsAI {
			continue
		}
		switch powerClass(sides.Stats[playerID], sides.Me) {
		case "power-much-up":
			view.Power.MuchUp++
		case "power-up":
			view.Power.Up++
		case "power-even":
			view.Power.Even++
		case "power-down":
			view.Power.Down++
		case "power-much-down":
			view.Power.MuchDown++
		default:
			view.Power.Unknown++
		}
	}
	view.Power.Me = sides.Me

	for playerID, c := range centers {
		owner := state.Players[playerID]
		name := nonEmpty(owner.Nation, owner.Name)
		if name == "" {
			continue
		}
		_, enemy := sides.Enemies[playerID]
		_, friend := sides.Friends[playerID]
		// Во врагах и в друзьях сразу человек быть не может, но если списки
		// разошлись, вражда важнее: о ней предупредить нужнее.
		mark := ""
		switch {
		case enemy:
			mark = "⚔"
			friend = false
		case friend:
			mark = "🤝"
		}
		// Ник под названием страны: страну помнят по нику, а не по стране,
		// и на карте одно без другого читается плохо. Повторять его
		// не нужно там, где он и есть подпись.
		nick := owner.Name
		if nick == name {
			nick = ""
		}
		x, y := c.point()
		view.Labels = append(view.Labels,
			mapLabel{X: x, Y: y, Mark: mark, Text: name, Nick: nick, Enemy: enemy, Friend: friend})
	}
	// Порядок подписей игра не задаёт, а страница должна быть одинаковой
	// от захода к заходу.
	sort.Slice(view.Labels, func(i, j int) bool { return view.Labels[i].Text < view.Labels[j].Text })
	return view
}

// center копит середину владений страны: среднее по точкам её контуров.
// Для подписи этого довольно, а честный центр масс многоугольников стоил бы
// заметно дороже при том же результате на глаз.
type center struct{ sumX, sumY, n int }

func (c *center) add(points []supremacy.Point) {
	for _, p := range points {
		c.sumX += p.X
		c.sumY += p.Y
		c.n++
	}
}

func (c *center) point() (int, int) {
	if c.n == 0 {
		return 0, 0
	}
	return c.sumX / c.n, c.sumY / c.n
}

// allianceFills раздаёт альянсам цвета: чем больше провинций у альянса
// в этой партии, тем раньше он берёт цвет из палитры. Порядок именно
// по владениям, а не по названию: крупный альянс на карте должен быть
// заметен, а мелкий не должен забирать яркий цвет. Альянсам, не влезшим
// в палитру, цвет не достаётся — они рисуются общим серым и остаются
// в легенде.
//
// Первый ответ — цвет по номеру игрока в партии, второй — легенда для
// страницы. Альянс берётся из самой Supremacy, а не из нашего справочника
// кланов: на карте должно быть видно, кто с кем в игре, а не кого мы
// как записали.
func allianceFills(l i18n.Lang, state *supremacy.GameState, alliances map[int]domain.Alliance) (map[int]string, []allianceLegendView) {
	memberOf := make(map[int]string, len(alliances))
	legend := map[string]*allianceLegendView{}
	for playerID, a := range alliances {
		if !a.InClan() {
			continue
		}
		memberOf[playerID] = a.ID
		if _, ok := legend[a.ID]; !ok {
			legend[a.ID] = &allianceLegendView{
				ID: a.ID, Name: nonEmpty(a.Name, l.T("game.noname")), Tag: a.Tag}
		}
	}
	if len(legend) == 0 {
		return nil, nil
	}

	for _, p := range state.Provinces {
		if id, ok := memberOf[p.Owner]; ok {
			legend[id].Provinces++
		}
	}

	out := make([]allianceLegendView, 0, len(legend))
	for _, a := range legend {
		out = append(out, *a)
	}
	// Порядок должен быть один и тот же от захода к заходу, поэтому
	// у равных по владениям альянсов решает название, а у одноимённых —
	// номер.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provinces != out[j].Provinces {
			return out[i].Provinces > out[j].Provinces
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})

	colors := make(map[string]string, len(out))
	for i := range out {
		if i < len(mapPalette) {
			out[i].Color = mapPalette[i]
			colors[out[i].ID] = out[i].Color
		}
	}

	fills := make(map[int]string, len(memberOf))
	for playerID, id := range memberOf {
		if color, ok := colors[id]; ok {
			fills[playerID] = color
		}
	}
	return fills, out
}

// topCapturedAt — когда снимали топ. Время у всех строк снимка одно,
// поэтому берётся у первой; пустой снимок отдаёт нулевое время, и страница
// о давности молчит.
func topCapturedAt(top []domain.TopAlliance) time.Time {
	if len(top) == 0 {
		return time.Time{}
	}
	return top[0].CapturedAt
}

// topFills отмечает игроков, чей клан стоит в верхушке рейтинга игры.
// Клан берётся тот же, что и в режиме «Кланы», — из самой Supremacy;
// топовым его делает только то, что он нашёлся в снимке рейтинга.
//
// Цвет раздаётся по местам в рейтинге, а не по владениям: в этом режиме
// вопрос не «кто больше занял», а «с кем имеешь дело», и первый номер
// должен выглядеть одинаково от партии к партии. Клан ниже палитры
// остаётся серым, но в легенде — как и в режиме кланов.
//
// Первый ответ — строка легенды по номеру игрока в партии, чтобы карта
// одним взглядом получила и цвет, и место; второй — сама легенда.
func topFills(l i18n.Lang, state *supremacy.GameState, alliances map[int]domain.Alliance,
	top map[string]domain.TopAlliance) (map[int]topLegendView, []topLegendView) {

	if len(top) == 0 {
		return nil, nil
	}

	memberOf := make(map[int]string)
	rows := map[string]*topLegendView{}
	for playerID, a := range alliances {
		if !a.InClan() {
			continue
		}
		t, ok := top[a.ID]
		if !ok {
			continue
		}
		memberOf[playerID] = a.ID
		row := rows[a.ID]
		if row == nil {
			// Имя берём из снимка рейтинга, а не из ответа про игрока:
			// в рейтинге оно заведомо есть, а у игрока может быть пустым.
			row = &topLegendView{
				ID: a.ID, Rank: t.Rank,
				Name: nonEmpty(t.Name, a.Name), Tag: nonEmpty(t.Tag, a.Tag),
			}
			rows[a.ID] = row
		}
		row.Players++
	}
	if len(rows) == 0 {
		return nil, nil
	}

	for _, p := range state.Provinces {
		if id, ok := memberOf[p.Owner]; ok {
			rows[id].Provinces++
		}
	}

	out := make([]topLegendView, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	// Мест в рейтинге двух одинаковых не бывает, но порядок всё равно
	// доопределяем номером клана: пустой снимок или сбой рейтинга не должны
	// делать страницу разной от захода к заходу.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		if i < len(mapPalette) {
			out[i].Color = mapPalette[i]
		}
	}

	fills := make(map[int]topLegendView, len(memberOf))
	byID := make(map[string]topLegendView, len(out))
	for _, row := range out {
		byID[row.ID] = row
	}
	for playerID, id := range memberOf {
		fills[playerID] = byID[id]
	}
	return fills, out
}

// teamFills раздаёт цвета коалициям партии. Придумывать их, в отличие
// от кланов, почти не приходится: и название, и цвет у коалиции свои,
// игровые — она живёт в самой партии, а не в нашем справочнике. Палитра
// подставляется только там, где игра цвета не дала.
//
// Первый ответ — цвет по номеру игрока в партии, второй — легенда.
// meID нужен, чтобы отметить в легенде коалицию смотрящего.
func teamFills(l i18n.Lang, state *supremacy.GameState, meID int) (map[int]string, []teamLegendView) {
	if len(state.Teams) == 0 {
		return nil, nil
	}

	members := make(map[int]int, len(state.Teams))
	for _, p := range state.Players {
		if _, ok := state.Teams[p.TeamID]; ok {
			members[p.TeamID]++
		}
	}
	if len(members) == 0 {
		return nil, nil
	}

	// Порядок тот же, что у кланов: крупнейшая коалиция сверху, а при
	// равенстве решает название — список не должен прыгать от захода
	// к заходу.
	ids := make([]int, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if members[a] != members[b] {
			return members[a] > members[b]
		}
		if state.Teams[a].Name != state.Teams[b].Name {
			return state.Teams[a].Name < state.Teams[b].Name
		}
		return a < b
	})

	mine := 0
	if meID > 0 {
		mine = state.Players[meID].TeamID
	}

	colors := make(map[int]string, len(ids))
	legend := make([]teamLegendView, 0, len(ids))
	for i, id := range ids {
		team := state.Teams[id]
		color := team.Color
		if color == "" {
			color = mapPalette[i%len(mapPalette)]
		}
		colors[id] = color
		legend = append(legend, teamLegendView{
			Name:    nonEmpty(team.Name, l.T("game.noname")),
			Color:   color,
			Members: members[id],
			Mine:    id == mine,
		})
	}

	fills := make(map[int]string, len(state.Players))
	for _, p := range state.Players {
		if color, ok := colors[p.TeamID]; ok {
			fills[p.ID] = color
		}
	}
	return fills, legend
}

// viewerPlayer — кто смотрящий в этой партии. Игрока ищем по игровому ID
// из его профиля, а не по state.Me: под общим аккаунтом в партию ходит
// админка, а страницу читает человек со своей страной. Пустой ID и чужая
// партия дают один ответ — «своего» здесь нет.
func viewerPlayer(state *supremacy.GameState, siteUserID string) (supremacy.Player, bool) {
	if siteUserID == "" {
		return supremacy.Player{}, false
	}
	for _, p := range state.Players {
		if p.SiteUserID == siteUserID {
			return p, true
		}
	}
	return supremacy.Player{}, false
}

// powerClass — какой полосой владелец отличается от смотрящего. Пусто,
// когда сравнивать не с чем: счёта нет у него, у нас или наша опасность
// нулевая — так бывает у того, кто ещё не воевал ни разу.
func powerClass(owner, me domain.UserStats) string {
	if !owner.Rated() || !me.Rated() || me.Danger() <= 0 {
		return ""
	}
	switch r := owner.Danger() / me.Danger(); {
	case r >= powerMuchUp:
		return "power-much-up"
	case r >= powerUp:
		return "power-up"
	case r >= powerEven:
		return "power-even"
	case r >= powerDown:
		return "power-down"
	default:
		return "power-much-down"
	}
}

// formatRatio печатает кд и опасность одинаково — с двумя знаками. Числа
// эти маленькие и разница между 1.2 и 1.25 в них существенна.
func formatRatio(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func svgPoints(points []supremacy.Point) string {
	var b strings.Builder
	for i, pt := range points {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Itoa(pt.X))
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(pt.Y))
	}
	return b.String()
}
