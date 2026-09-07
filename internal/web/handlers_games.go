package web

import (
	"context"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/supremacy"
)

// gamesTimeout ограничивает поход в игру: страницу открывает живой человек
// и ждать он готов недолго, а API Supremacy иногда отвечает минутами.
const gamesTimeout = 20 * time.Second

// gameSource — откуда берём игры аккаунта. Интерфейс, а не *supremacy.Client,
// чтобы страницу можно было проверить без похода в сеть. Пустое значение
// означает, что аккаунт игры не настроен (S1914_USER не задан).
type gameSource interface {
	MyGames(ctx context.Context) ([]supremacy.Game, error)
	Game(ctx context.Context, gameID string) (*supremacy.Game, []supremacy.GameLogin, error)
	// StateFor — состояние партии из общего кэша или с игрового сервера,
	// вместе с временем съёмки копии; StateFresh — сколько эта копия
	// считается свежей. Оба знают про скорость партии и про то, что руту
	// нужно свежее остальных.
	StateFor(ctx context.Context, gameID string, ours, root bool) (*supremacy.GameState, time.Time, error)
	StateFresh(gameID string, root bool) time.Duration
	DeployInfantry(ctx context.Context, gameID string) (string, error)
	MapGeometry(ctx context.Context, mapID string) (*supremacy.MapGeometry, error)
	UserID() string
}

// gameView — строка таблицы. Игра отдаёт всё строками, включая время,
// поэтому разбор дат делаем здесь, а не в шаблоне.
type gameView struct {
	ID       string
	Title    string
	State    string
	Day      string
	Players  string
	Language string
	PlayerID string
	Started  time.Time
	Joined   time.Time
}

// gamesList — раздел «Игры». Руту он показывает активные партии общего аккаунта,
// остальным — только форму проверки: список партий говорит, где аккаунт
// играет прямо сейчас, и это знание рутовое. Проверить же партию по номеру
// может любой, кому доступ к разделу выдан: и сведения о партии, и её состав
// с картой берутся так, что заходом в партию это не считается.
func (s *Server) gamesList(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	data := map[string]any{
		"Games": nil, "Error": "", "PlayURL": "", "CheckError": "",
		"Query": strings.TrimSpace(r.URL.Query().Get("id")),
		// Coalitions — сводка архива и состояние его рубильника. Только
		// руту: обход ходит в игру от общего аккаунта проекта.
		"Coalitions": nil,
		// Checked — партии, которые смотрел тот, кто сейчас на странице,
		// и через сколько дней они из списка уходят.
		"Checked": nil, "CheckedDays": int(domain.CheckedGamesTTL.Hours() / 24),
		// Проверки карты: сколько их в сутки и сколько осталось на сегодня.
		// Руту не считают — ему и показывать нечего.
		"ChecksTotal": 0, "ChecksLeft": 0, "ChecksCounted": false,
	}

	// Список проверенных партий читается из базы и есть у всех, кому открыт
	// раздел: он ничего не спрашивает у игры, поэтому считается до неё —
	// даже когда игровой аккаунт не настроен, список остаётся на месте.
	checked, err := s.checked.List(r.Context(),
		currentUser(r).ID, time.Now().Add(-domain.CheckedGamesTTL))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Checked"] = checked

	// Остаток проверок на сегодня — там же, откуда партии открывают.
	if me := currentUser(r); !me.IsRoot() {
		left, err := s.checks.Left(r.Context(), me.ID, me.MapChecks, repo.Day(time.Now()))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["ChecksCounted"] = true
		data["ChecksTotal"] = me.MapChecks
		data["ChecksLeft"] = left
	}

	if s.games == nil {
		data["Error"] = lang.T("games.noaccount")
		s.render(w, r, http.StatusOK, "games", data)
		return
	}
	if msg := r.URL.Query().Get("err"); msg != "" {
		data["CheckError"] = lang.T("games.badnumber")
	}

	// Не рут дальше формы не идёт: за списком партий мы ради него в игру
	// не ходим вовсе.
	if !currentUser(r).IsRoot() {
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	// Архив коалиций читается из базы и молчания игры не боится, поэтому
	// считается до похода за списком партий: даже когда игра не ответит,
	// рубильник останется на месте.
	if s.coalitions != nil && s.settings != nil {
		view, err := s.coalitionCard(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Coalitions"] = view
	}

	games, err := s.games.MyGames(ctx)
	if err != nil {
		// Отказ игры — это не поломка админки: показываем причину на самой
		// странице, чтобы было видно, когда протухла сессия или лежит API.
		s.log.Error("список игр Supremacy", "err", err)
		data["Error"] = lang.T("games.list.error", err)
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	data["Games"] = gameViews(lang, games)
	data["PlayURL"] = supremacy.PlayURL(s.games.UserID())
	s.render(w, r, http.StatusOK, "games", data)
}

var reDigits = regexp.MustCompile(`\d+`)

// gameIDFrom достаёт номер партии из того, что ввели. Приносят и голый номер,
// и ссылку целиком, поэтому берём самое длинное число: в адресе игры есть
// и другие — хотя бы 1914 в самом имени сайта. Короткие обрывки за номер
// не считаем вовсе.
func gameIDFrom(input string) string {
	var best string
	for _, digits := range reDigits.FindAllString(input, -1) {
		if len(digits) > len(best) {
			best = digits
		}
	}
	if len(best) < 4 {
		return ""
	}
	return best
}

// gamesCheck — кнопка «Проверить игру»: по введённому номеру уводит
// на страницу партии. Отдельный роут нужен затем, чтобы адрес партии
// оставался прежним и им можно было делиться. Что покажет страница, решает
// она сама: в чужую партию она заглянет наблюдателем сразу, в свою — только
// по кнопке, потому что своя требует настоящего входа.
func (s *Server) gamesCheck(w http.ResponseWriter, r *http.Request) {
	id := gameIDFrom(r.URL.Query().Get("id"))
	if id == "" {
		http.Redirect(w, r, "/games?err=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/games/"+id, http.StatusSeeOther)
}

// gameViews готовит игры к показу: свежая партия сверху.
func gameViews(l i18n.Lang, games []supremacy.Game) []gameView {
	out := make([]gameView, 0, len(games))
	for _, g := range games {
		out = append(out, gameView{
			ID:       g.GameID,
			Title:    g.Title,
			State:    gameState(l, g.State),
			Day:      g.DayOfGame,
			Players:  g.NrOfPlayers,
			Language: g.Language,
			PlayerID: g.PlayerID,
			Started:  g.Started(),
			Joined:   g.Joined(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// gameState переводит состояние партии. Незнакомое оставляем как есть:
// лучше показать слово игры, чем скрыть его пустотой.
func gameState(l i18n.Lang, state string) string {
	switch state {
	case "running":
		return l.T("gamestate.running")
	case "readytojoin":
		return l.T("gamestate.readytojoin")
	case "finished", "ended":
		return l.T("gamestate.finished")
	}
	return state
}

// mapShape — одна провинция на рисунке карты. Ни заливки, ни обводки здесь
// нет: карта рисуется один раз, а красится по-разному в зависимости от
// выбранного режима, поэтому провинция несёт признаки сразу всех режимов,
// а цвет из них выбирает css.
//
// Class — «neutral» у ничейной земли, «clan» у той, чей клан получил цвет,
// «top» у страны игрока из клана-топа, «side-enemy» и «side-friend» у стран
// из личных списков смотрящего, «team» у состоящей в коалиции, «premium»
// у страны игрока с подпиской, «own» у своей: эта последняя пометка одна
// на все режимы, потому что обводка везде означает одно и то же.
// ClanColor, TeamColor и TopColor — цвета клана, коалиции и клана из топа,
// они же css-переменные --clan, --team и --top; пусто, если красить нечем.
type mapShape struct {
	Points    string
	Class     string
	ClanColor template.CSS
	TeamColor template.CSS
	TopColor  template.CSS
	Title     string
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

// gameRelationView — игрок партии, оказавшийся в личном списке смотрящего.
// Card — его карточка в базе, по ней и нашли. Premium — подписка
// «Высокое командование»: у союзника и у врага она одинаково важна,
// поэтому едет в оба списка.
type gameRelationView struct {
	Nation  string
	Name    string
	Premium bool
	Card    *domain.Player
}

// gamePlayerView — игрок партии сам по себе, без всякой связи с базой.
// Нужен там, где страница перечисляет участников: например, у кого
// в этой партии премиум.
type gamePlayerView struct {
	Nation string
	Name   string
	Mine   bool
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
	}
	// Середина владений каждой страны — там и будет её имя.
	centers := map[int]*center{}

	for _, p := range state.Provinces {
		points, ok := geo.Land[p.ID]
		if !ok {
			continue
		}

		shape := mapShape{Points: svgPoints(points), Title: p.Name}
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
			// Премиум — свойство самого игрока, из состояния партии.
			// В своём режиме он и есть вся разметка.
			if owner.Premium {
				classes = append(classes, "premium")
				shape.Title += ", " + l.T("game.premium")
			}
			if owner.Banned {
				shape.Title += ", " + l.T("game.bannedone")
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

// gameAlliances выясняет, кто из игроков партии в каком клане Supremacy.
// В сеть отсюда не ходим совсем: страница читает то, что уже лежит в базе,
// а незнакомых игроков просто записывает в очередь — их спросит воркер.
// Поэтому карта открывается ровно за то же время, что и без кланов, а у
// новой партии первый заход показывает часть игроков серыми.
//
// Второй ответ — сколько игроков партии ещё не спрошено: об этом стоит
// сказать на странице, иначе серый цвет читается как «клана нет».
func (s *Server) gameAlliances(ctx context.Context, state *supremacy.GameState,
	roster []supremacy.GameLogin) (map[int]domain.Alliance, int, error) {

	byUser := make(map[string][]int, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			byUser[p.SiteUserID] = append(byUser[p.SiteUserID], p.ID)
		}
	}
	if len(byUser) == 0 {
		return nil, 0, nil
	}

	ids := make([]string, 0, len(byUser))
	for id := range byUser {
		ids = append(ids, id)
	}

	known, err := s.alliances.Known(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	// Состав, только что взятый у сайта, свежее любой записи в базе
	// и перекрывает её.
	for _, a := range allianceRows(roster, time.Now()) {
		known[a.SiteUserID] = a
	}

	// В очередь попадают те, о ком не сказал ни состав, ни база. После
	// того как кланы приезжают вместе с партией, таких почти не остаётся:
	// разве что игрок пришёл в партию после того, как сайт отдал состав.
	pending := 0
	queue := make([]string, 0, len(ids))
	for _, id := range ids {
		if a, ok := known[id]; !ok || !a.Known() {
			pending++
			queue = append(queue, id)
		}
	}
	if err := s.alliances.Enqueue(ctx, queue); err != nil {
		return nil, 0, err
	}

	// Один игрок сайта в партии бывает ровно один раз, но связывать
	// правильнее через список: партия ключуется номером в ней, а не
	// номером на сайте.
	out := make(map[int]domain.Alliance, len(known))
	for userID, a := range known {
		for _, playerID := range byUser[userID] {
			out[playerID] = a
		}
	}
	return out, pending, nil
}

// allianceRows переводит состав партии в записи о кланах. Тега сайт здесь
// не даёт — он приходит только с составом самого клана, — и пустой тег
// затирать чужую находку не должен: об этом знает сама запись в базе.
func allianceRows(roster []supremacy.GameLogin, at time.Time) []domain.Alliance {
	out := make([]domain.Alliance, 0, len(roster))
	for _, l := range roster {
		if !l.Known() {
			continue
		}
		row := domain.Alliance{SiteUserID: l.SiteUserID, CheckedAt: &at}
		if l.InClan() {
			row.ID, row.Name = l.AllianceID, l.AllianceName
		}
		out = append(out, row)
	}
	return out
}

// rememberAlliances кладёт кланы из состава партии в базу и ставит новые
// кланы в очередь на состав. Это главный способ пополнения: одна проверка
// партии приносит кланы всех её участников разом, тогда как воркер
// спрашивает по игроку за раз.
func (s *Server) rememberAlliances(ctx context.Context, roster []supremacy.GameLogin) error {
	rows := allianceRows(roster, time.Now())
	if len(rows) == 0 {
		return nil
	}
	if err := s.alliances.Save(ctx, rows, time.Now()); err != nil {
		return err
	}

	seen := map[string]bool{}
	var clans []string
	for _, r := range rows {
		if r.InClan() && !seen[r.ID] {
			seen[r.ID] = true
			clans = append(clans, r.ID)
		}
	}
	return s.alliances.EnqueueAlliances(ctx, clans)
}

// gameSides — что мы знаем об игроках партии помимо состояния самой партии.
// Ключ везде один: номер игрока в этой партии. Cards — карточки из базы,
// Enemies и Friends — те же карточки, отобранные личными списками
// смотрящего, Alliances — кланы игроков в самой Supremacy, по ним карта
// и красится.
type gameSides struct {
	Cards     map[int]*domain.Player
	Enemies   map[int]*domain.Player
	Friends   map[int]*domain.Player
	Alliances map[int]domain.Alliance
	// Top — верхушка рейтинга кланов по номеру клана. Общая на всю
	// админку, а не на партию: место в рейтинге принадлежит клану.
	Top map[string]domain.TopAlliance
}

// gameLists сводит игроков партии с базой по игровому ID и раскладывает их
// по личным спискам смотрящего. Карточки берутся одним запросом на всё:
// и на раскраску по кланам, и на оба списка.
func (s *Server) gameLists(r *http.Request, state *supremacy.GameState) (*gameSides, error) {
	ids := make([]string, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			ids = append(ids, p.SiteUserID)
		}
	}

	cards, err := s.players.ByGameIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	list := make([]*domain.Player, 0, len(cards))
	for _, c := range cards {
		list = append(list, c)
	}
	enemyIDs, err := s.enemies.marked(r, list)
	if err != nil {
		return nil, err
	}
	friendIDs, err := s.friends.marked(r, list)
	if err != nil {
		return nil, err
	}

	// Один и тот же разбор для всех трёх наборов: игроки партии сверяются
	// с карточками, карточки — с личной пометкой. Пустая пометка означает
	// «берём все найденные карточки».
	pick := func(marked map[int64]bool) map[int]*domain.Player {
		out := make(map[int]*domain.Player)
		for _, p := range state.Players {
			card, ok := cards[p.SiteUserID]
			if !ok || (marked != nil && !marked[card.ID]) {
				continue
			}
			out[p.ID] = card
		}
		return out
	}
	return &gameSides{
		Cards:   pick(nil),
		Enemies: pick(enemyIDs),
		Friends: pick(friendIDs),
	}, nil
}

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

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// relationViews — список для страницы: кто из партии есть в личном списке.
func relationViews(state *supremacy.GameState, found map[int]*domain.Player) []gameRelationView {
	out := make([]gameRelationView, 0, len(found))
	for playerID, card := range found {
		p := state.Players[playerID]
		out = append(out, gameRelationView{
			Nation: p.Nation, Name: p.Name, Premium: p.Premium, Card: card})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// premiumViews — у кого в партии премиум. Игра сообщает подписку про всех
// участников, и это единственное место, где её видно: в самом клиенте
// чужой премиум ничем не отмечен. Компьютерных игроков пропускаем —
// подписки у них не бывает, а список должен читаться.
func premiumViews(l i18n.Lang, state *supremacy.GameState, meID int) []gamePlayerView {
	var out []gamePlayerView
	for _, p := range state.Players {
		if !p.Premium || p.IsAI {
			continue
		}
		out = append(out, gamePlayerView{
			Nation: nonEmpty(p.Nation, l.T("game.unknown")), Name: p.Name, Mine: p.ID == meID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// rememberPlayers записывает состав партии в базу: ник и бан у каждого,
// кто пришёл с номером на сайте. Свойства партии — поражение, выход, премиум
// — сюда не идут: они про эту игру, а не про человека, и в другой партии
// будут другими.
//
// Ошибка записи страницу не отменяет: мы уже в партии, состав перед глазами,
// и терять его показ из-за базы было бы обидно.
func (s *Server) rememberPlayers(ctx context.Context, state *supremacy.GameState) error {
	list := make([]domain.GamePlayer, 0, len(state.Players))
	now := time.Now()
	for _, p := range state.Players {
		if p.SiteUserID == "" {
			continue
		}
		list = append(list, domain.GamePlayer{
			SiteUserID: p.SiteUserID, Nickname: p.Name, Banned: p.Banned,
			SeenAt: now, SeenGameID: state.GameID,
		})
	}
	return s.gamePlayers.Save(ctx, list)
}

// bannedViews — кто из игроков партии забанен. Игра сообщает бан про всех
// участников, и это единственное место, где его видно.
func bannedViews(l i18n.Lang, state *supremacy.GameState) []gamePlayerView {
	var out []gamePlayerView
	for _, p := range state.Players {
		if !p.Banned {
			continue
		}
		out = append(out, gamePlayerView{
			Nation: nonEmpty(p.Nation, l.T("game.unknown")), Name: p.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// rosterView — строка состава партии: то, что сайт рассказывает про
// участника, плюс то, что о нём знаем мы. Card — карточка из базы, если
// игрок в ней есть; Enemy и Friend — личные списки смотрящего.
//
// Team — номер коалиции, как его отдаёт сайт. Названия коалиций он не даёт,
// они видны только изнутри партии, поэтому номер здесь работает как метка
// «эти трое заодно».
type rosterView struct {
	Login  string
	Clan   string
	Team   string
	Level  int
	Card   *domain.Player
	Enemy  bool
	Friend bool
}

// rosterViews готовит состав партии к показу и сводит его с базой по
// игровому ID: тот самый номер, что сайт зовёт siteUserID. Порядок — по
// клану, чтобы соклановцы стояли рядом, а внутри клана по нику; безкланные
// уходят в конец.
func (s *Server) rosterViews(r *http.Request, roster []supremacy.GameLogin) ([]rosterView, error) {
	lang := langOf(r)
	if len(roster) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(roster))
	for _, l := range roster {
		if l.Known() {
			ids = append(ids, l.SiteUserID)
		}
	}
	cards, err := s.players.ByGameIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	list := make([]*domain.Player, 0, len(cards))
	for _, c := range cards {
		list = append(list, c)
	}
	enemies, err := s.enemies.marked(r, list)
	if err != nil {
		return nil, err
	}
	friends, err := s.friends.marked(r, list)
	if err != nil {
		return nil, err
	}

	out := make([]rosterView, 0, len(roster))
	for _, l := range roster {
		view := rosterView{Login: l.Login, Level: l.Level}
		if l.InClan() {
			view.Clan = nonEmpty(l.AllianceName, lang.T("tip.clan", l.AllianceID))
		}
		if l.TeamID != "" && l.TeamID != "0" {
			view.Team = l.TeamID
		}
		if card, ok := cards[l.SiteUserID]; ok {
			view.Card = card
			view.Enemy, view.Friend = enemies[card.ID], friends[card.ID]
		}
		out = append(out, view)
	}

	sortRoster(out)
	return out, nil
}

// sortRoster ставит состав в том порядке, в котором его читают: соклановцы
// рядом, а безкланные в конце — они друг другу никто, и разбирать их стоит
// после того, как видны стороны. Внутри клана порядок по нику.
func sortRoster(list []rosterView) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.Clan == "") != (b.Clan == "") {
			return a.Clan != ""
		}
		if a.Clan != b.Clan {
			return a.Clan < b.Clan
		}
		return strings.ToLower(a.Login) < strings.ToLower(b.Login)
	})
}

// gameAbout и gameLive — ответы двух походов наружу, которые страница партии
// делает одновременно. Каналы под них буферизованные: если страница сдалась
// раньше (сайт молчит, номер выдуман), горутина допишет ответ в буфер
// и завершится, а не повиснет навсегда.
type gameAbout struct {
	game   *supremacy.Game
	roster []supremacy.GameLogin
	err    error
}

type gameLive struct {
	state *supremacy.GameState
	// at — когда сняли копию состояния. Может быть заметно раньше запроса:
	// копия общая, и привезти её мог кто угодно.
	at  time.Time
	err error
}

// setMapWait кладёт на страницу таймер до следующей копии: секунды тикает
// скрипт, готовую строку видит тот, у кого скрипта нет.
func setMapWait(data map[string]any, left time.Duration) {
	sec := waitSeconds(left)
	data["MapWait"] = sec
	data["MapWaitText"] = waitClock(sec)
}

// gameCard — страница одной партии: что это за партия и что админка делает
// в ней сама. Состав партии сюда не тянется по умолчанию: заход на игровой
// сервер игра засчитывает как вход в партию, поэтому его просят отдельно.
func (s *Server) gameCard(w http.ResponseWriter, r *http.Request) {
	s.renderGame(w, r, r.PathValue("id"), r.URL.Query().Get("state") == "1", "")
}

func (s *Server) renderGame(w http.ResponseWriter, r *http.Request, gameID string, withState bool, errMsg string) {
	// Засекаем сразу: «собираем данные» показывается ровно столько, сколько
	// не хватило странице до обычного времени сбора. См. loaderMs.
	started := time.Now()

	if s.games == nil {
		s.render(w, r, http.StatusOK, "game", map[string]any{
			"GameID": gameID,
			"Error":  langOf(r).T("games.noaccount"),
		})
		return
	}

	lang := langOf(r)

	task, err := s.tasks.Get(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := map[string]any{
		"GameID": gameID, "Task": task, "Error": errMsg,
		"Interval": s.heroEvery, "Game": nil, "State": nil, "Mine": nil,
		"Map": nil, "MapError": "", "Enemies": nil, "Friends": nil,
		"Premium": nil, "Banned": nil, "Roster": nil,
		// AlliancesPending — сколько игроков партии воркер ещё не спросил.
		"AlliancesPending": 0,
		// Pairs — кто из игроков этой партии уже союзничал раньше.
		"Pairs": nil,
		// Ours — играет ли общий аккаунт в этой партии. От этого зависит,
		// как мы смотрим на неё: в свою заходим игроком по кнопке, в чужую
		// — наблюдателем и сразу, потому что входом в партию это не считается.
		"Ours": false,
		// Своя страна — от профиля смотрящего. Её может не быть по двум
		// разным причинам, и сказать об этом надо по-разному.
		"Me": nil, "NoProfileGameID": false, "NotPlaying": false,
		// Когда сняли копию состояния и сколько ждать новой: секундами
		// для скрипта, строкой для глаз. Плюс адрес, по которому просить
		// её заново.
		"MapAt": time.Time{}, "MapWait": 0, "MapWaitText": "",
		// Проверки: сколько их в сутки у смотрящего, сколько осталось,
		// считают ли их ему вообще и не кончились ли они прямо сейчас.
		"ChecksTotal": 0, "ChecksLeft": 0, "ChecksCounted": false, "ChecksOut": false,
		// LoaderMs — сколько держать «собираем данные». Ноль — не держать.
		"LoaderMs": 0,
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	// Три похода наружу независимы, а суммарно занимают почти три секунды,
	// поэтому идут разом. Сведения о партии с сайта — самый долгий из них
	// (около двух секунд), и ждать его, ничего не делая, обиднее всего.
	about := make(chan gameAbout, 1)
	go func() {
		g, roster, err := s.games.Game(ctx, gameID)
		about <- gameAbout{game: g, roster: roster, err: err}
	}()

	// Название и день берём из дешёвого списка партий: он же подтверждает,
	// что партия наша и ещё идёт.
	games, err := s.games.MyGames(ctx)
	if err != nil {
		data["Error"] = joinErrors(errMsg, lang.T("games.list.error", err))
		s.render(w, r, http.StatusOK, "game", data)
		return
	}
	// mine — сама партия из списка своих, а не только её вид: архиву
	// коалиций нужна скорость, а её вид не несёт.
	var mine *supremacy.Game
	for i, g := range games {
		if g.GameID == gameID {
			mine = &games[i]
			view := gameViews(lang, []supremacy.Game{g})[0]
			data["Game"] = &view
			data["Ours"] = true
			break
		}
	}
	ours := data["Ours"] == true

	// Состояние партии — третий поход, и ему хватает уже известного: свою
	// партию открываем игроком, чужую смотрим наблюдателем. Отправляем его
	// сейчас, чтобы он шёл под ответ сайта, а не после него.
	//
	// Цена решения: по выдуманному номеру партии мы теперь сходим в игру
	// зря — сайт скажет «такой нет» уже после. Один лишний запрос на опечатку
	// против секунды на каждом честном открытии.
	//
	// Показ карты стоит проверки: рут выдаёт их на сутки, и тратятся они
	// на каждый показ — даже когда данные пришли из общего кэша. Человек
	// смотрит партию, а откуда взялись данные, его не касается.
	who := currentUser(r)
	root := who.IsRoot()
	day := repo.Day(time.Now())
	data["ChecksCounted"] = !root
	data["ChecksTotal"] = who.MapChecks

	var (
		live  chan gameLive
		spent bool
	)
	switch {
	case withState || !ours:
		if !root {
			ok, left, err := s.checks.Spend(ctx, who.ID, who.MapChecks, day)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			data["ChecksLeft"] = left
			if !ok {
				// Проверки кончились — карты не будет. Остальное собирается
				// ниже: сведения с сайта и состав ничего не стоят.
				data["ChecksOut"] = true
				break
			}
			spent = true
		}
		live = make(chan gameLive, 1)
		go func() {
			state, at, err := s.games.StateFor(ctx, gameID, ours, root)
			live <- gameLive{state: state, at: at, err: err}
		}()
	case !root:
		// В свою партию не ходили — её открывают кнопкой. Остаток проверок
		// надо показать до того, как человек в него упрётся.
		left, err := s.checks.Left(ctx, who.ID, who.MapChecks, day)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["ChecksLeft"] = left
	}

	// Состав сайт отдаёт вместе с самой партией и про любую партию, поэтому
	// спрашиваем всегда: в нём кланы всех участников, а это вся раскраска
	// карты — и её не приходится собирать по игроку за раз.
	got := <-about
	game, roster, err := got.game, got.roster, got.err
	if err != nil {
		s.log.Warn("сведения о партии", "gameID", gameID, "err", err)
		// Своя партия переживёт молчание сайта: название и день уже есть
		// из списка, а кланы возьмутся из базы. Чужая — нет, про неё мы
		// больше ничего и не знаем.
		if data["Game"] == nil {
			data["Error"] = joinErrors(errMsg, lang.T("game.notfound", gameID, err))
			s.render(w, r, http.StatusOK, "game", data)
			return
		}
		data["Error"] = joinErrors(errMsg, lang.T("game.roster.error", err))
	}

	// Кланы из состава кладём в базу независимо от того, дошло ли дело
	// до карты: раз уж сайт их назвал, пусть остаются.
	if err := s.rememberAlliances(ctx, roster); err != nil {
		s.log.Error("запись кланов из состава", "gameID", gameID, "err", err)
	}

	// Состав показываем там, где нет карты: в чужой партии он и есть вся
	// проверка, а в своей — то, что видно до захода внутрь.
	views, err := s.rosterViews(r, roster)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Roster"] = views

	// Чужая партия — не ошибка: название и состояние сайт называет про любую.
	if !ours {
		view := gameViews(lang, []supremacy.Game{*game})[0]
		data["Game"] = &view
	}

	// Партия нашлась — значит, она проверена, и место ей в личном списке
	// того, кто смотрит. Отметка ставится и на чужую партию, и на свою
	// до захода внутрь: проверкой считается сам взгляд на партию, а не то,
	// как далеко он зашёл. Название пишем то, что видно сейчас: партия
	// когда-нибудь кончится, а список должен читаться и без похода в игру.
	if view, ok := data["Game"].(*gameView); ok {
		if err := s.checked.Mark(ctx, who.ID, gameID, view.Title, time.Now()); err != nil {
			// Список — удобство, а не суть страницы: не вышло записать,
			// значит партия просто не всплывёт наверх.
			s.log.Error("запись проверенной партии", "gameID", gameID, "err", err)
		}
	}

	// В свою партию заходим только по кнопке: такой заход игра засчитывает
	// как вход. В чужую смотрим наблюдателем, и вот это уже ничего не стоит
	// — значит, и прятать за кнопкой незачем.
	if live != nil {
		res := <-live
		state, err := res.state, res.err
		if err != nil {
			// Проверку возвращаем: человек ничего не увидел, брать за это
			// плату нечестно. Не вышло вернуть — не беда, скажем в лог.
			if spent {
				if err := s.checks.Refund(ctx, who.ID, day); err != nil {
					s.log.Error("возврат проверки карты", "user_id", who.ID, "err", err)
				}
				data["ChecksLeft"] = data["ChecksLeft"].(int) + 1
			}
			s.log.Warn("состояние партии", "gameID", gameID, "наша", ours, "err", err)
			if ours {
				data["Error"] = joinErrors(errMsg, lang.T("game.enter.error", err))
			} else {
				data["Error"] = joinErrors(errMsg, lang.T("game.observe.error", err))
			}
			s.render(w, r, http.StatusOK, "game", data)
			return
		}
		data["State"] = state
		// Копия общая, и снята она могла быть заметно раньше — скажем,
		// когда именно, и когда можно будет взять новую.
		data["MapAt"] = res.at
		setMapWait(data, time.Until(res.at.Add(s.games.StateFresh(gameID, root))))

		// Своя страна ищется по игровому ID из профиля: у каждого смотрящего
		// она своя, а у кого-то её в этой партии и нет вовсе.
		viewer := who.GameID
		me, playing := viewerPlayer(state, viewer)
		meID := 0
		switch {
		case playing:
			meID = me.ID
			mine := state.Owned(meID)
			sort.Slice(mine, func(i, j int) bool {
				if mine[i].Capital != mine[j].Capital {
					return mine[i].Capital
				}
				return mine[i].Name < mine[j].Name
			})
			player := me
			data["Me"] = &player
			data["Mine"] = mine
		case viewer == "":
			data["NoProfileGameID"] = true
		default:
			data["NotPlaying"] = true
		}

		// Карту рисуем, если получилось: очертания лежат отдельным файлом
		// на static-сервере игры, и его недоступность не повод прятать
		// остальную страницу.
		// Игроков партии сводим с базой по игровому ID и отмечаем тех,
		// кто в личных списках того, кто смотрит.
		sides, err := s.gameLists(r, state)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Enemies"] = relationViews(state, sides.Enemies)
		data["Premium"] = premiumViews(lang, state, meID)
		data["Banned"] = bannedViews(lang, state)

		// Раз уж состав перед глазами — запомним про игроков то,
		// что принадлежит им самим, а не этой партии.
		if err := s.rememberPlayers(ctx, state); err != nil {
			s.log.Error("запись состава партии", "gameID", gameID, "err", err)
		}
		data["Friends"] = relationViews(state, sides.Friends)

		// Кланы игроков берутся из самой игры, но не сейчас: страница
		// читает уже известное, а незнакомых ставит в очередь воркеру.
		alliances, pending, err := s.gameAlliances(ctx, state, roster)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		sides.Alliances = alliances
		data["AlliancesPending"] = pending

		// Верхушка рейтинга — десяток строк на всю админку, поэтому
		// читается целиком и на каждую страницу. Её отсутствие карту
		// не ломает: режим топа просто останется серым.
		top, err := s.topAlliances.All(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		sides.Top = make(map[string]domain.TopAlliance, len(top))
		for _, t := range top {
			sides.Top[t.ID] = t
		}
		at := topCapturedAt(top)
		data["TopAt"] = at
		data["TopKnown"] = !at.IsZero()

		// Коалиции этой партии оседают в архиве: состояние всё равно перед
		// глазами. Сведения о партии берём откуда есть — у своей они из
		// списка своих, у чужой из ответа сайта.
		about := game
		if about == nil {
			about = mine
		}
		s.rememberCoalitions(ctx, about, state)

		// И сразу обратный вопрос к архиву: кто из здешних уже союзничал.
		pairs, err := s.coalitionPairs(r, state, sides)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Pairs"] = pairs

		if geo, err := s.games.MapGeometry(ctx, state.MapID); err != nil {
			s.log.Warn("очертания карты", "gameID", gameID, "mapID", state.MapID, "err", err)
			data["MapError"] = lang.T("game.map.error", err)
		} else {
			data["Map"] = gameMap(lang, geo, state, sides, meID)
			data["LoaderMs"] = loaderMs(time.Since(started))
		}
	}

	s.render(w, r, http.StatusOK, "game", data)
}

// gameHeroToggle включает и выключает автопризыв пехоты в партии.
func (s *Server) gameHeroToggle(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	on := r.PostFormValue("on") == "1"

	title := r.PostFormValue("title")
	if err := s.tasks.SetHeroDeploy(r.Context(), gameID, title, on, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("автопризыв пехоты переключён",
		"gameID", gameID, "включён", on, "user_id", currentUser(r).ID)

	s.renderGame(w, r, gameID, false, "")
}

// gameHeroRun — «призвать сейчас»: то же самое, что делает воркер, но по
// нажатию. Ждать до его тика, чтобы проверить одну кнопку, незачем, и итог
// пишется в те же поля — страница показывает последний заход независимо
// от того, кто его сделал.
func (s *Server) gameHeroRun(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	if s.games == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("games.noaccount.short"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	var msg string
	result, err := s.games.DeployInfantry(ctx, gameID)
	if err != nil {
		s.log.Error("призыв пехоты вручную", "gameID", gameID, "err", err)
		result, msg = err.Error(), err.Error()
	} else {
		s.log.Info("призыв пехоты вручную", "gameID", gameID, "user_id", currentUser(r).ID, "итог", result)
	}

	// Запись итога некритична: сам призыв уже случился (или не случился),
	// и терять из-за базы ответ человеку было бы обидно.
	if err := s.tasks.MarkHeroRun(r.Context(), gameID, time.Now(), result); err != nil {
		s.log.Error("запись итога призыва", "gameID", gameID, "err", err)
	}

	s.renderGame(w, r, gameID, false, msg)
}

// joinErrors склеивает предупреждение и ошибку: на странице место одно,
// а сказать иногда нужно и то и другое.
func joinErrors(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "; ")
}
