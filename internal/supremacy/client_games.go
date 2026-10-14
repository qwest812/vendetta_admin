package supremacy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Game — одна игра из списка. Числа игра отдаёт строками, оставляем как есть:
// нам они нужны для лога и сравнения, а не для арифметики.
type Game struct {
	GameID      string `json:"gameID"`
	Title       string `json:"title"`
	Comment     string `json:"comment"`
	ScenarioID  string `json:"scenarioID"`
	OpenSlots   string `json:"openSlots"`
	NrOfPlayers string `json:"nrofplayers"`
	DayOfGame   string `json:"dayofgame"`
	State       string `json:"state"`
	Language    string `json:"language"`
	PasswordSet string `json:"passwordset"`
	Ranked      string `json:"ranked"`
	StartOfGame string `json:"startofgame2"`
	MinRank     string `json:"minRank"`
	// TimeScale — во сколько игровое время медленнее ускоренного предела:
	// 1 у обычной партии, 0.25 у «[Speed]», 0.1 у «[Event]».
	TimeScale looseFloat `json:"timeScale"`
	// AnonymousRound — событийная партия с анонимным раундом: в состоянии
	// такой партии игра подменяет имя каждого участника на «анонимный».
	// Ключ лежит в тех же свойствах партии, что и timeScale, и клиент игры
	// читает его так же — см. IsAnonymous.
	AnonymousRound looseFlag `json:"anonymousRound"`

	// Приходят только в списке своих игр: наш номер в партии и когда мы
	// в неё вошли. У игр из лобби они пустые.
	PlayerID string `json:"playerID"`
	JoinTime string `json:"joinTime"`
}

// UnmarshalJSON — разбор, терпимый к тому, что игра шлёт число вместо строки.
// Подробности у looseUnmarshal.
func (g *Game) UnmarshalJSON(b []byte) error {
	// plain — та же структура без этого метода, иначе разбор зациклится.
	type plain Game
	return looseUnmarshal(b, (*plain)(g))
}

// looseUnmarshal заворачивает в кавычки числа, попавшие в строковые поля,
// и только потом разбирает. Один и тот же ключ игра шлёт то так, то эдак:
// в лобби startofgame2 приходит как "1785920128", а в списке своих игр —
// как 1785920128; siteUserID и allianceID у обычного игрока строки, а у
// удалённого — ноль числом. Одна такая мелочь роняла разбор целиком:
// не поле терялось, а весь список игр или весь состав партии.
//
// Чинить это здесь дешевле, чем заводить свой тип на каждый капризный ключ:
// поля структур остаются строками, а новые ключи лечатся сами. Числовые
// поля (playerLevel, timeScale) не трогаем — их разбирают как числа.
func looseUnmarshal(b []byte, v any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}

	asString := stringFields(reflect.TypeOf(v).Elem())
	for k, raw := range fields {
		if asString[k] && isJSONNumber(raw) {
			fields[k] = strconv.AppendQuote(nil, string(raw))
		}
	}

	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, v)
}

// stringFields — имена ключей json, которые структура держит строками.
func stringFields(t reflect.Type) map[string]bool {
	out := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		if name != "-" {
			out[name] = true
		}
	}
	return out
}

func isJSONNumber(v json.RawMessage) bool {
	if len(v) == 0 {
		return false
	}
	return v[0] == '-' || (v[0] >= '0' && v[0] <= '9')
}

// looseFlag — признак «да/нет», который игра шлёт то единицей, то строкой,
// то булевым. Разбирается терпимо и не ошибается никогда: ключ для нас новый,
// а уронить им разбор всего лобби мы права не имеем — как это бывает,
// написано у looseUnmarshal. Незнакомое значение означает «нет»: не открыть
// лишнего важнее, чем угадать.
type looseFlag bool

func (f *looseFlag) UnmarshalJSON(b []byte) error {
	switch strings.Trim(string(b), `"`) {
	case "1", "true":
		*f = true
	default:
		*f = false
	}
	return nil
}

// looseFloat — число, которое игра шлёт то так, то эдак: в списке лобби
// timeScale приходит строкой «0.25», а в свойствах одной партии — числом
// 0.25. Один и тот же ключ, два разных вида, и выбирать не нам.
type looseFloat float64

func (n *looseFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = looseFloat(f)
	return nil
}

// IsAnonymous — анонимная ли это партия. Клиент игры решает так же:
// свойство anonymousRound, приведённое к числу, равно единице.
//
// В такой партии игра называет «анонимным» каждого участника, кто бы он
// ни был. Скрыт при этом только ник: номер аккаунта в состоянии настоящий,
// и состав с сайта по нему называет живого человека.
func (g *Game) IsAnonymous() bool { return bool(g.AnonymousRound) }

// Speed — во сколько раз партия быстрее обычной: 1, 2, 4, 10. Игра называет
// обратную величину, а считать и группировать удобнее по скорости: «в x4»
// читается, «в 0.25» — нет.
//
// Округление здесь не косметика. Игра шлёт timeScale приближённо — у x10 это
// 0.10000000149011612, и деление даёт 9.99999985. По такой скорости партии
// группируются в аналитике, и без округления одна и та же x10 разъезжалась бы
// на несколько групп.
//
// Ноль у timeScale означает, что поля не было: такую партию считаем обычной —
// это худшее, что может случиться от догадки.
func (g Game) Speed() float64 {
	if g.TimeScale <= 0 {
		return 1
	}
	speed := math.Round(1 / float64(g.TimeScale))
	if speed < 1 {
		return 1
	}
	return speed
}

// Started и Joined — время начала партии и нашего входа в неё. Игра шлёт
// секунды строкой; ноль и мусор одинаково означают «неизвестно», и разбирать
// это стоит здесь, у самой партии, а не у каждого, кто её показывает.
func (g Game) Started() time.Time { return unixSeconds(g.StartOfGame) }
func (g Game) Joined() time.Time  { return unixSeconds(g.JoinTime) }

func unixSeconds(s string) time.Time {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// OpenGames возвращает игры из лобби, куда сейчас можно вступить.
func (c *Client) OpenGames(ctx context.Context) ([]Game, error) {
	const perPage = 100

	var out []Game
	for page := 1; ; page++ {
		raw, err := c.call(ctx, "getGames", []param{
			{"numEntriesPerPage", strconv.Itoa(perPage)},
			{"page", strconv.Itoa(page)},
			{"lang", c.lang},
			{"password", "0"},
			{"allianceGame", "0"},
			{"openSlots", "1"},
			{"isFilterSearch", "false"},
			{"global", "1"},
			{"loadUserLoginData", "1"},
		})
		if err != nil {
			return nil, err
		}

		var res struct {
			NumGames int `json:"numGames"`
			Games    []struct {
				Properties Game `json:"properties"`
			} `json:"games"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("разбор списка игр: %w", err)
		}
		for _, g := range res.Games {
			out = append(out, g.Properties)
			c.noteSpeeds(g.Properties)
		}
		// Страница пришла неполной или мы уже собрали всё обещанное —
		// дальше листать нечего.
		if len(res.Games) < perPage || len(out) >= res.NumGames {
			return out, nil
		}
	}
}

// GameLogin — участник партии по данным сайта. Приходит вместе с самой
// партией и содержит то, чего в состоянии на игровом сервере нет: клан
// игрока с названием и его уровень. Пустой AllianceID (сайт шлёт «0»)
// означает, что в клане игрок не состоит.
//
// Всё это сайт отдаёт про любую партию, не спрашивая, играем ли мы в ней:
// состав, кланы и коалиции узнаются без всякого захода на игровой сервер.
type GameLogin struct {
	Login        string `json:"login"`
	SiteUserID   string `json:"siteUserID"`
	AllianceID   string `json:"allianceID"`
	AllianceName string `json:"allianceName"`
	TeamID       string `json:"teamID"`
	Level        int    `json:"playerLevel"`
}

// InClan — состоит ли игрок в клане. Ноль у сайта означает «ни в каком»,
// и отличить его от пустоты стоит здесь, а не у каждого вызывающего.
func (l GameLogin) InClan() bool { return l.AllianceID != "" && l.AllianceID != "0" }

// Known — настоящий ли это игрок сайта. У удалённого аккаунта siteUserID
// приходит нулём, и спрашивать про такого клан или заводить ему карточку
// нечего: все удалённые в партии слились бы в одного игрока с номером «0».
// В составе партии они при этом остаются и место в ней занимают.
func (l GameLogin) Known() bool { return l.SiteUserID != "" && l.SiteUserID != "0" }

// UnmarshalJSON — тот же терпимый разбор, что и у Game: у удалённых игроков
// siteUserID и allianceID приходят нулём-числом вместо строки.
func (l *GameLogin) UnmarshalJSON(b []byte) error {
	type plain GameLogin
	return looseUnmarshal(b, (*plain)(l))
}

// Game спрашивает у сайта одну партию по её номеру — любую, не только свою.
// Отвечает сайт из общего списка партий, поэтому заходом в партию это
// не считается и работает для чужих игр тоже: по номеру можно узнать
// название, сценарий, день, число игроков и весь состав с кланами,
// ничего в партию не отправляя.
//
// Ответ держим в кэше: сайт отдаёт его медленно и тем медленнее, чем
// больше партия. Партию на 500 игроков он собирает секунд двадцать,
// и всё это время страница стоит и ждёт. Подробности у rosterTTL.
func (c *Client) Game(ctx context.Context, gameID string) (*Game, []GameLogin, error) {
	entry := c.gameEntry(gameID)

	// Спрашиваем по одному на партию: иначе двое, открывшие её разом,
	// ждут по двадцать секунд каждый и дёргают сайт дважды вместо раза.
	// Ждём под ctx, а не на мьютексе: если страница, стоящая второй,
	// уже никому не нужна, держать её незачем.
	select {
	case entry.busy <- struct{}{}:
		defer func() { <-entry.busy }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	if game, logins, ok := entry.fresh(); ok {
		return game, logins, nil
	}

	game, logins, err := c.fetchGame(ctx, gameID)
	if err != nil {
		return nil, nil, err
	}
	entry.game, entry.logins, entry.at = game, logins, time.Now()
	c.noteSize(gameID, len(logins))

	// Отдаём через тот же fresh, что и попадание в кэш: первый
	// спрашивающий должен получить ровно то же, что и все следующие.
	game, logins, _ = entry.fresh()
	return game, logins, nil
}

// CachedGame — ответ сайта о партии, если он уже лежит в кэше и не протух.
// В сеть не ходит и не ждёт: страница по нему решает, ждать ли сайт вообще.
// Пока ответ кто-то везёт, его здесь нет — занятую ячейку не трогаем, её
// поля пишет тот, кто везёт.
func (c *Client) CachedGame(gameID string) (*Game, []GameLogin, bool) {
	// Ячейку не заводим: пустая живёт до первого удачного ответа, а про
	// эту партию, может, никто так и не спросит.
	c.mu.Lock()
	entry, ok := c.games[gameID]
	c.mu.Unlock()
	if !ok {
		return nil, nil, false
	}
	select {
	case entry.busy <- struct{}{}:
		defer func() { <-entry.busy }()
	default:
		return nil, nil, false
	}
	return entry.fresh()
}

// noteSize запоминает состав партии числом. Ответ целиком протухает, а это
// число — нет: партия не усохнет вдвое, и следующий раз можно сразу ждать
// столько, сколько она заслуживает.
func (c *Client) noteSize(gameID string, players int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sizes == nil {
		c.sizes = make(map[string]int)
	}
	c.sizes[gameID] = players
}

// GameSize — сколько игроков в партии, если мы о ней уже спрашивали.
// false вторым значением значит «не спрашивали ни разу»; такую партию
// зовущий должен считать большой, пока не выяснится обратное: узнать
// размер заранее неоткуда, а ошибиться в сторону терпения дешевле.
func (c *Client) GameSize(gameID string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.sizes[gameID]
	return n, ok
}

// rosterTTL — сколько ответ сайта о партии считается свежим. Полчаса тут
// не осторожность, а осознанный размен: за это время в партии успевает
// смениться разве что чей-то альянс, а стоит каждый такой вопрос двадцати
// секунд ожидания на самой большой партии.
//
// bigRosterTTL — то же для больших партий, и размен там другой: ждать
// приходится до минуты, а меняется за полдня всё равно немногое. Шесть
// часов выбраны так, чтобы за рабочий день партию спросили раз, много два.
const (
	rosterTTL    = 30 * time.Minute
	bigRosterTTL = 6 * time.Hour
)

// BigRoster — с какого состава партия считается большой. Сотня — это граница,
// за которой сайт заметно задумывается: тридцать игроков он отдаёт за секунду,
// четыреста — за двадцать. Число одно на всю админку: по нему выбирается
// и срок кэша, и сколько ждать ответа, и говорить ли об этом человеку.
const BigRoster = 100

// gameEntry — ячейка кэша под одну партию. Заодно выбрасываем протухшие:
// ячейка живёт до следующего вопроса о любой партии, а вопросов этих
// столько же, сколько открытий страницы.
func (c *Client) gameEntry(gameID string) *gameEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.games == nil {
		c.games = make(map[string]*gameEntry)
	}
	for id, e := range c.games {
		// Пустое время — ячейка, которую прямо сейчас заполняют: её
		// выбрасывать нельзя, иначе спрашивающий останется ни с чем.
		if id != gameID && !e.at.IsZero() && time.Since(e.at) > e.ttl() {
			delete(c.games, id)
		}
	}
	if e, ok := c.games[gameID]; ok {
		return e
	}
	e := &gameEntry{busy: make(chan struct{}, 1)}
	c.games[gameID] = e
	return e
}

// gameEntry — что сайт рассказал об одной партии и когда. Читают и пишут
// его под busy, поэтому своего замка полям не нужно.
type gameEntry struct {
	busy   chan struct{}
	game   *Game
	logins []GameLogin
	at     time.Time
}

// ttl — сколько держать эту ячейку. Большую партию держим дольше: ответ
// по ней и стоит дороже, и устаревает не быстрее.
func (e *gameEntry) ttl() time.Duration {
	if len(e.logins) >= BigRoster {
		return bigRosterTTL
	}
	return rosterTTL
}

// fresh отдаёт ответ, если он ещё не протух. Партию отдаём копией — она
// маленькая, а вот делить один указатель между страницами не стоит. Состав
// общий: он только читается, как и очертания карты.
func (e *gameEntry) fresh() (*Game, []GameLogin, bool) {
	if e.game == nil || time.Since(e.at) > e.ttl() {
		return nil, nil, false
	}
	game := *e.game
	return &game, e.logins, true
}

func (c *Client) fetchGame(ctx context.Context, gameID string) (*Game, []GameLogin, error) {
	raw, err := c.call(ctx, "getGame", []param{{"gameID", gameID}})
	if err != nil {
		return nil, nil, err
	}

	var res struct {
		Properties Game        `json:"properties"`
		Logins     []GameLogin `json:"logins"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("разбор партии %s: %w", gameID, err)
	}
	if res.Properties.GameID == "" {
		return nil, nil, fmt.Errorf("партии %s у игры нет", gameID)
	}
	c.noteSpeeds(res.Properties)
	return &res.Properties, res.Logins, nil
}

// MyGames возвращает игры аккаунта, которые идут сейчас, — то же, что
// показывает вкладка «Обзор» на /game.php. Завершённые сюда не попадают:
// они лежат в архиве, а это отдельный вызов с mygamesMode=archived.
func (c *Client) MyGames(ctx context.Context) ([]Game, error) {
	// Спрашиваем по одному, как и про отдельную партию: страницу могли
	// открыть разом несколько человек, и незачем спрашивать сайт за каждого.
	// Ждём под ctx: тому, кто уже не нужен, стоять в очереди незачем.
	select {
	case c.mine.busy <- struct{}{}:
		defer func() { <-c.mine.busy }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if games, ok := c.mine.fresh(); ok {
		return games, nil
	}

	// userID нужен уже в параметрах, поэтому сессию получаем заранее.
	// Если её перебьёт перезаход внутри call, ничего не сломается: аккаунт
	// тот же, а значит и userID тот же.
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := c.call(ctx, "getGames", myGamesParams(sess.userID))
	if err != nil {
		return nil, err
	}
	games, err := parseMyGames(raw)
	if err != nil {
		return nil, err
	}
	c.noteSpeeds(games...)

	c.mine.games, c.mine.at = games, time.Now()
	// Отдаём через тот же fresh, что и попадание в кэш: первый спрашивающий
	// должен получить ровно то же, что и все следующие.
	games, _ = c.mine.fresh()
	return games, nil
}

// myGamesTTL — сколько список своих партий считается свежим. Список нужен
// не только разделу «Игры»: страница любой партии спрашивает его, чтобы
// понять, играем мы в ней или смотрим со стороны, — и вот это уже запрос
// на каждый просмотр, растущий с числом смотрящих, а не партий. Минута
// выбрана так, чтобы сотня открытий подряд стоила одного запроса, а только
// что начатая партия признавалась своей почти сразу.
const myGamesTTL = time.Minute

// minesEntry — кэш списка своих партий: сам список, время ответа и очередь
// к сайту. Устроен как gameEntry, разница только в том, что партия здесь
// не одна и ключа у ячейки нет.
type minesEntry struct {
	busy  chan struct{}
	games []Game
	at    time.Time
}

// fresh отдаёт список копией: страницы держат в руках ссылки на его строки,
// и делить одну память между ними не стоит.
func (e *minesEntry) fresh() ([]Game, bool) {
	if e.games == nil || time.Since(e.at) > myGamesTTL {
		return nil, false
	}
	return slices.Clone(e.games), true
}

// myGamesParams вынесены отдельно, чтобы порядок параметров можно было
// сверить с подписанным вектором из HAR, не выходя в сеть.
func myGamesParams(userID string) []param {
	return []param{
		{"userID", userID},
		{"loadUserLoginData", "1"},
	}
}

// parseMyGames разбирает ответ на список своих игр. В отличие от лобби
// result здесь — сразу массив игр, без обёртки с numGames.
func parseMyGames(raw json.RawMessage) ([]Game, error) {
	var res []struct {
		Properties Game `json:"properties"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор списка своих игр: %w", err)
	}
	out := make([]Game, 0, len(res))
	for _, g := range res {
		out = append(out, g.Properties)
	}
	return out, nil
}
