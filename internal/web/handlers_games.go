package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
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
	GameState(ctx context.Context, gameID string) (*supremacy.GameState, error)
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

// gamesList — активные игры того аккаунта, под которым админка ходит
// в Supremacy 1914. Аккаунт в проекте один и общий, поэтому список у всех
// одинаковый; доступ к разделу выдаёт рут поимённо.
func (s *Server) gamesList(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Games": nil, "Error": "", "PlayURL": ""}

	if s.games == nil {
		data["Error"] = "Аккаунт Supremacy 1914 не настроен: задайте S1914_USER и S1914_PASSWORD."
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	games, err := s.games.MyGames(ctx)
	if err != nil {
		// Отказ игры — это не поломка админки: показываем причину на самой
		// странице, чтобы было видно, когда протухла сессия или лежит API.
		s.log.Error("список игр Supremacy", "err", err)
		data["Error"] = "Не удалось получить список игр: " + err.Error()
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	data["Games"] = gameViews(games)
	data["PlayURL"] = supremacy.PlayURL(s.games.UserID())
	s.render(w, r, http.StatusOK, "games", data)
}

// gameViews готовит игры к показу: свежая партия сверху.
func gameViews(games []supremacy.Game) []gameView {
	out := make([]gameView, 0, len(games))
	for _, g := range games {
		out = append(out, gameView{
			ID:       g.GameID,
			Title:    g.Title,
			State:    gameState(g.State),
			Day:      g.DayOfGame,
			Players:  g.NrOfPlayers,
			Language: g.Language,
			PlayerID: g.PlayerID,
			Started:  unixTime(g.StartOfGame),
			Joined:   unixTime(g.JoinTime),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// gameState переводит состояние партии. Незнакомое оставляем как есть:
// лучше показать английское слово, чем скрыть его пустотой.
func gameState(state string) string {
	switch state {
	case "running":
		return "идёт"
	case "readytojoin":
		return "набор"
	case "finished", "ended":
		return "завершена"
	}
	return state
}

// unixTime разбирает время игры: секунды строкой, ноль и мусор означают
// «неизвестно».
func unixTime(s string) time.Time {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// mapShape — одна провинция на рисунке карты.
type mapShape struct {
	Points string
	Fill   string
	Stroke string
	Title  string
}

// mapLabel — подпись страны на карте: имя пишется один раз, посередине
// её владений. Enemy и Friend — из личных списков смотрящего, и вместе они
// не встречаются: один и тот же игрок либо там, либо там.
//
// Mark — значок перед названием (меч или рукопожатие). Он хранится отдельно
// от имени, потому что красится по-своему: страница рисует его собственным
// tspan.
type mapLabel struct {
	X      int
	Y      int
	Mark   string
	Text   string
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
}

// gameRelationView — игрок партии, оказавшийся в личном списке смотрящего.
// Card — его карточка в базе, по ней и нашли.
type gameRelationView struct {
	Nation string
	Name   string
	Card   *domain.Player
}

// Цвета для тех, у кого своего нет: ничейная земля и игрок без цвета
// в профиле. Море рисуется фоном, отдельными фигурами его не набираем.
const (
	mapNeutralFill = "#2b3a46"
	mapUnknownFill = "#4a5b68"
	mapOwnStroke   = "#e8b45f"
)

// Игра раздаёт странам чистые кричащие цвета: карта целиком из них слепит,
// и поверх неё плохо читаются и названия, и обводки. Поэтому каждый цвет
// уводим к фону карты — mapFade говорит, насколько сильно.
const (
	mapSeaFill = "#16222c"
	mapFade    = 0.45
)

// gameMap раскрашивает очертания карты по владельцам из состояния партии.
// Провинции, которых нет в геометрии, просто не рисуются: файл карты общий
// для всех партий и меняется отдельно от них.
// meID — номер смотрящего в этой партии, его провинции обводим отдельно.
// Ноль означает, что своей страны здесь нет: обводить нечего. Обводка только
// одна, своя: страны из личных списков и без неё видны по значку и цвету
// названия, а лишние рамки на карте только рябят.
func gameMap(geo *supremacy.MapGeometry, state *supremacy.GameState,
	enemies, friends map[int]*domain.Player, meID int) *gameMapView {

	view := &gameMapView{Width: geo.Width, Height: geo.Height}
	// Середина владений каждой страны — там и будет её имя.
	centers := map[int]*center{}

	for _, p := range state.Provinces {
		points, ok := geo.Land[p.ID]
		if !ok {
			continue
		}

		shape := mapShape{Points: svgPoints(points), Fill: mapNeutralFill, Title: p.Name}
		if p.Owner > 0 {
			owner := state.Players[p.Owner]
			shape.Fill = mapUnknownFill
			if owner.Color != "" {
				shape.Fill = fadeColor(owner.Color)
			}
			shape.Title = p.Name + " — " + nonEmpty(owner.Nation, "неизвестно")
			if owner.Name != "" {
				shape.Title += " (" + owner.Name + ")"
			}
			// Личные списки на заливку не влияют, но в подсказке про них
			// сказать стоит: наводят как раз чтобы понять, чья провинция.
			if _, ok := friends[p.Owner]; ok {
				shape.Title += ", в друзьях"
			}
			if _, ok := enemies[p.Owner]; ok {
				shape.Title += ", во врагах"
			}

			c := centers[p.Owner]
			if c == nil {
				c = &center{}
				centers[p.Owner] = c
			}
			c.add(points)
		}
		// Свои провинции обводим: на политической карте иначе не найти,
		// чей цвет наш.
		if meID > 0 && p.Owner == meID {
			shape.Stroke = mapOwnStroke
		}
		view.Shapes = append(view.Shapes, shape)
	}

	for playerID, c := range centers {
		owner := state.Players[playerID]
		name := nonEmpty(owner.Nation, owner.Name)
		if name == "" {
			continue
		}
		_, enemy := enemies[playerID]
		_, friend := friends[playerID]
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
		x, y := c.point()
		view.Labels = append(view.Labels,
			mapLabel{X: x, Y: y, Mark: mark, Text: name, Enemy: enemy, Friend: friend})
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

// gameLists сводит игроков партии с базой по игровому ID и раскладывает их
// по личным спискам смотрящего: сначала враги, потом друзья. Ключ — номер
// игрока в партии. Карточки берутся одним запросом на оба списка.
func (s *Server) gameLists(r *http.Request, state *supremacy.GameState) (
	map[int]*domain.Player, map[int]*domain.Player, error) {

	ids := make([]string, 0, len(state.Players))
	for _, p := range state.Players {
		if p.SiteUserID != "" {
			ids = append(ids, p.SiteUserID)
		}
	}

	cards, err := s.players.ByGameIDs(r.Context(), ids)
	if err != nil {
		return nil, nil, err
	}
	list := make([]*domain.Player, 0, len(cards))
	for _, c := range cards {
		list = append(list, c)
	}
	enemyIDs, err := s.enemies.marked(r, list)
	if err != nil {
		return nil, nil, err
	}
	friendIDs, err := s.friends.marked(r, list)
	if err != nil {
		return nil, nil, err
	}

	// Один и тот же разбор для обоих списков: игроки партии сверяются
	// с карточками, карточки — с личной пометкой.
	pick := func(marked map[int64]bool) map[int]*domain.Player {
		out := make(map[int]*domain.Player)
		for _, p := range state.Players {
			if card, ok := cards[p.SiteUserID]; ok && marked[card.ID] {
				out[p.ID] = card
			}
		}
		return out
	}
	return pick(enemyIDs), pick(friendIDs), nil
}

// fadeColor уводит цвет страны к фону карты: игра шлёт «rgb(230,190,140)»,
// и в чистом виде такая заливка спорит с подписями и обводками. Чужую
// запись возвращаем нетронутой — нарисовать ярко лучше, чем потерять цвет.
func fadeColor(color string) string {
	r, g, b, ok := parseRGB(color)
	if !ok {
		return color
	}
	sr, sg, sb, ok := parseHexRGB(mapSeaFill)
	if !ok {
		return color
	}
	mix := func(c, sea int) int {
		return int(float64(c)*(1-mapFade) + float64(sea)*mapFade + 0.5)
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", mix(r, sr), mix(g, sg), mix(b, sb))
}

// parseRGB разбирает «rgb(r,g,b)» — в таком виде цвета отдаёт supremacy.
func parseRGB(color string) (int, int, int, bool) {
	inside, ok := strings.CutPrefix(strings.TrimSpace(color), "rgb(")
	if !ok {
		return 0, 0, 0, false
	}
	inside, ok = strings.CutSuffix(inside, ")")
	if !ok {
		return 0, 0, 0, false
	}
	parts := strings.Split(inside, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return 0, 0, 0, false
		}
		v[i] = n
	}
	return v[0], v[1], v[2], true
}

// parseHexRGB разбирает «#rrggbb» — так записаны наши собственные цвета.
func parseHexRGB(color string) (int, int, int, bool) {
	hex, ok := strings.CutPrefix(color, "#")
	if !ok || len(hex) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16), int(n >> 8 & 0xff), int(n & 0xff), true
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
		out = append(out, gameRelationView{Nation: p.Nation, Name: p.Name, Card: card})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nation < out[j].Nation })
	return out
}

// gameCard — страница одной партии: что это за партия и что админка делает
// в ней сама. Состав партии сюда не тянется по умолчанию: заход на игровой
// сервер игра засчитывает как вход в партию, поэтому его просят отдельно.
func (s *Server) gameCard(w http.ResponseWriter, r *http.Request) {
	s.renderGame(w, r, r.PathValue("id"), r.URL.Query().Get("state") == "1", "")
}

func (s *Server) renderGame(w http.ResponseWriter, r *http.Request, gameID string, withState bool, errMsg string) {
	if s.games == nil {
		s.render(w, r, http.StatusOK, "game", map[string]any{
			"GameID": gameID,
			"Error":  "Аккаунт Supremacy 1914 не настроен: задайте S1914_USER и S1914_PASSWORD.",
		})
		return
	}

	task, err := s.tasks.Get(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := map[string]any{
		"GameID": gameID, "Task": task, "Error": errMsg,
		"Interval": s.heroEvery, "Game": nil, "State": nil, "Mine": nil,
		"Map": nil, "MapError": "", "Enemies": nil, "Friends": nil,
		// Своя страна — от профиля смотрящего. Её может не быть по двум
		// разным причинам, и сказать об этом надо по-разному.
		"Me": nil, "NoProfileGameID": false, "NotPlaying": false,
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	// Название и день берём из дешёвого списка партий: он же подтверждает,
	// что партия наша и ещё идёт.
	games, err := s.games.MyGames(ctx)
	if err != nil {
		data["Error"] = joinErrors(errMsg, "Не удалось получить список игр: "+err.Error())
		s.render(w, r, http.StatusOK, "game", data)
		return
	}
	for _, g := range gameViews(games) {
		if g.ID == gameID {
			view := g
			data["Game"] = &view
			break
		}
	}
	if data["Game"] == nil {
		data["Error"] = joinErrors(errMsg, "Партии "+gameID+" нет среди активных партий аккаунта.")
		s.render(w, r, http.StatusOK, "game", data)
		return
	}

	if withState {
		state, err := s.games.GameState(ctx, gameID)
		if err != nil {
			data["Error"] = joinErrors(errMsg, "Не удалось зайти в партию: "+err.Error())
			s.render(w, r, http.StatusOK, "game", data)
			return
		}
		data["State"] = state

		// Своя страна ищется по игровому ID из профиля: у каждого смотрящего
		// она своя, а у кого-то её в этой партии и нет вовсе.
		viewer := currentUser(r).GameID
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
		enemies, friends, err := s.gameLists(r, state)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Enemies"] = relationViews(state, enemies)
		data["Friends"] = relationViews(state, friends)

		if geo, err := s.games.MapGeometry(ctx, state.MapID); err != nil {
			s.log.Warn("очертания карты", "gameID", gameID, "mapID", state.MapID, "err", err)
			data["MapError"] = "Карту нарисовать не вышло: " + err.Error()
		} else {
			data["Map"] = gameMap(geo, state, enemies, friends, meID)
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
		s.renderGame(w, r, gameID, false, "Аккаунт Supremacy 1914 не настроен.")
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
