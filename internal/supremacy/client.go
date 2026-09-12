// Package supremacy — клиент к web-API игры Supremacy 1914 (Bytro).
//
// Авторизация в этом API не куковая: каждый вызов подписан sha1 от склейки
// ключа API, имени действия, параметров и uberAuthHash. Сам uberAuthHash
// выдаётся страницей /game.php после обычного логина по паролю и живёт
// вместе с сессией, поэтому клиент держит его у себя и обновляет перезаходом.
//
// Порядок параметров важен: он одинаков в подписи и в теле запроса, а вот
// кодирование разное — в теле значения url-кодированы, в подписи сырые.
package supremacy

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	baseURL        = "https://www.supremacy1914.com"
	apiKey         = "ingameS1914Ultimate"
	apiVersion     = "20141208"
	trackingSource = "browser-desktop"

	// Ходим под тем же UA, что и настоящий клиент: лишний повод отличаться
	// от браузера нам не нужен.
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"
)

// Client ходит в API от имени одного игрового аккаунта.
// Безопасен для конкурентного использования.
type Client struct {
	http *http.Client
	log  *slog.Logger
	user string
	pass string
	lang string

	mu   sync.Mutex
	sess *session
	// loginDone — идущая сейчас попытка входа: канал закрывается, когда
	// она чем-нибудь кончилась. Пусто — никто не входит. Так вход остаётся
	// один на всех (два одновременных вызова с протухшей подписью входили бы
	// дважды подряд), но ждущий при этом волен уйти по своему сроку.
	loginDone chan struct{}
	// attemptErr — чем кончилась последняя попытка. Ждавшие её читают
	// отсюда: канал говорит «кончилась», а чем именно — вот это.
	attemptErr error
	// gsVer — версия клиента, которую сейчас требует игровой сервер.
	// Пустая означает «ещё не уточняли», см. gsVersionDefault.
	gsVer string
	// maps — очертания карт, скачанные с static-сервера. Файл на карту один
	// и не меняется, так что держим его до перезапуска.
	maps map[string]*MapGeometry
	// games — ответы сайта о партиях: сведения и состав. Живут недолго,
	// см. rosterTTL.
	games map[string]*gameEntry
	// states — состояния партий с игрового сервера: копия одна на всех,
	// кто бы её ни привёз. Подробности в state_cache.go.
	states map[string]*stateEntry
	// speeds — скорость увиденных партий: по ней считается, сколько копия
	// состояния считается свежей.
	speeds map[string]float64
	// sessions — где подпись лежит между перезапусками. Может быть пустым:
	// тогда каждый запуск начинается с входа, как раньше.
	sessions SessionStore
	// restored — подпись с прошлого запуска уже пробовали доставать. Ровно
	// один раз за процесс: если она не подошла, второй раз не подойдёт тоже.
	restored bool
	// Неудачный вход помним: сайт после отказа какое-то время отказывает
	// всем подряд, и ломиться в него каждым вызовом — верный способ
	// продлить отказ. См. loginBackoff.
	loginFails  int
	loginNextAt time.Time
	loginErr    error
	// mine — список партий аккаунта. Свой на всё приложение, потому что
	// и аккаунт один; см. myGamesTTL.
	mine *minesEntry
	// sizes — сколько игроков в партиях, о которых мы уже спрашивали.
	// Живёт дольше кэша ответов: сам ответ протухает, а размер партии —
	// нет, и по нему решают, сколько ждать сайт в следующий раз.
	sizes map[string]int
}

// session — то, что даёт странице право подписывать вызовы API.
type session struct {
	userID     string
	authHash   string
	authTstamp string
}

// SavedSession — та же подпись, но наружу: её кладут в базу, чтобы
// перезапуск приложения не означал нового входа в игру.
type SavedSession struct {
	UserID     string
	AuthHash   string
	AuthTstamp string
	SavedAt    time.Time
}

// SessionStore — где подпись лежит между запусками. Интерфейс, а не тип
// из repo: клиенту незачем знать про базу, а тестам — поднимать её.
type SessionStore interface {
	LoadSession(ctx context.Context) (SavedSession, bool, error)
	SaveSession(ctx context.Context, s SavedSession) error
}

// httpTimeout — предел на один запрос к сайту игры, и это именно предел,
// а не срок: сроки у зовущих свои и приходят контекстом. Держать его надо
// выше самого длинного из них (страница большой партии ждёт минуту),
// иначе он рубит раньше и вместо честного ожидания выходит обман: страница
// говорит «не ответил за минуту», а ждала на самом деле тридцать секунд.
const httpTimeout = 90 * time.Second

// loginTimeout — сколько отводится самой попытке входа. Свой срок ей нужен
// потому, что чужие сроки разные: страница ждёт секунды, воркер — сколько
// понадобится, а вход у них общий.
const loginTimeout = 45 * time.Second

// sessionReuse — насколько старую подпись ещё имеет смысл пробовать.
// Сколько она живёт на самом деле, знает только сервер игры и говорит
// об этом лишь отказом; сутки — граница, за которой попытка почти наверняка
// впустую, а стоит она лишнего вызова и всё равно кончается входом.
const sessionReuse = 24 * time.Hour

// ErrLoginPaused — вход отложен после неудач подряд. Зовущему это говорит
// больше, чем текст ошибки: раз войти нельзя, то и остальные вызовы этого
// круга не пройдут, и повторять их незачем.
var ErrLoginPaused = errors.New("вход в игру отложен")

// loginBackoff — сколько ждать после неудачного входа, по числу неудач
// подряд. Сайт игры на частые входы отвечает отказом всем подряд, и тогда
// каждый вызов воркера превращался в новую попытку: за минуту их набегали
// десятки, и отказ от этого только затягивался. Последнее значение
// повторяется, пока вход не удастся.
var loginBackoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

// Коды ответа игры; взяты из таблицы ResultCode в клиентском app.js.
// Перечислены только те, что нам важны: первые два лечатся перезаходом,
// остальные — нет, и повторять запрос бессмысленно.
const (
	codeSessionExpired       = -17
	codeAuthenticationFailed = -18
	codeNoSuchAlliance       = -23
)

// ErrNoSuchAlliance — клана на сайте больше нет: распался или переименовался
// в новый номер. Отличать этот отказ от временных важно потому, что повтор
// не поможет никогда, а не «пока»: клан с таким ответом надо убирать
// из очереди, иначе он будет занимать в ней место вечно.
var ErrNoSuchAlliance = errors.New("клана нет на сайте")

// APIError — игра ответила, но отказала.
type APIError struct {
	Action  string
	Code    int
	Message string
	Status  int

	// Malformed — вместо JSON пришло что-то другое. Обычно это HTML
	// страницы входа, куда нас увёл редирект после протухшей сессии.
	Malformed bool
}

// needsRelogin отвечает, поможет ли перезаход. Помимо явных кодов протухания
// сюда попадают ответы, где игра вместо JSON отдала что-то своё: так выглядит
// редирект на страницу входа.
func (e *APIError) needsRelogin() bool {
	switch {
	case e.Code == codeSessionExpired, e.Code == codeAuthenticationFailed:
		return true
	case e.Status == http.StatusUnauthorized, e.Status == http.StatusForbidden:
		return true
	case e.Malformed:
		return true
	default:
		return false
	}
}

// Unwrap подставляет опознанным кодам общую ошибку, чтобы зовущий разбирал
// их через errors.Is, а не сверял числа у себя. Сам текст отказа при этом
// остаётся: обёртки нет, подменяется только то, с чем сравнивают.
func (e *APIError) Unwrap() error {
	if e.Code == codeNoSuchAlliance {
		return ErrNoSuchAlliance
	}
	return nil
}

func (e *APIError) Error() string {
	switch {
	case e.Status != 0 && e.Status != http.StatusOK:
		return fmt.Sprintf("%s: http %d", e.Action, e.Status)
	case e.Malformed:
		return fmt.Sprintf("%s: ответ не разобрался как JSON: %s", e.Action, e.Message)
	default:
		return fmt.Sprintf("%s: resultCode=%d %s", e.Action, e.Code, e.Message)
	}
}

// NewClient готовит клиента, но в сеть ещё не ходит — логин случится
// при первом вызове.
//
// lang выбирает языковой раздел лобби, и это не косметика: в «ru» и «en»
// висят разные игры с одинаковыми названиями. Пустое значение — «ru».
func NewClient(user, pass, lang string, log *slog.Logger) *Client {
	if lang == "" {
		lang = "ru"
	}
	return &Client{
		mine: &minesEntry{busy: make(chan struct{}, 1)},
		http: &http.Client{Timeout: httpTimeout},
		log:  log,
		user: user,
		pass: pass,
		lang: lang,
	}
}

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

// PlayURL — ссылка, по которой в браузере открывается игра. Конкретную игру
// она не выбирает: uid здесь — это аккаунт, а не игра, так что открывшемуся
// клиенту игру придётся найти в списке самому. Пустой userID (до логина)
// просто опускает параметр.
func PlayURL(userID string) string {
	u := baseURL + "/game.php?bust=1"
	if userID != "" {
		u += "&uid=" + url.QueryEscape(userID)
	}
	return u
}

func (c *Client) gameVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gsVer == "" {
		return gsVersionDefault
	}
	return c.gsVer
}

func (c *Client) setGameVersion(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gsVer = v
}

// UserID — аккаунт, под которым клиент ходит в игру. До первого удачного
// логина пусто.
func (c *Client) UserID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == nil {
		return ""
	}
	return c.sess.userID
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

// Alliance — клан игрока на сайте игры. Это не наш справочник кланов:
// альянс заведён в самой Supremacy, игроки состоят в нём по-настоящему,
// и меняется он без нашего ведома.
type Alliance struct {
	ID   string
	Name string
	Tag  string
}

// UserAlliance спрашивает у сайта, в каком альянсе состоит игрок. Работает
// по тому же номеру, который партия отдаёт как siteUserID, поэтому состав
// партии сводится с альянсами без всякой базы.
//
// Заходом в партию этот вызов не считается: он идёт на сайт, а не на игровой
// сервер. Ответ nil без ошибки означает, что игрок ни в каком альянсе
// не состоит.
func (c *Client) UserAlliance(ctx context.Context, siteUserID string) (*Alliance, error) {
	raw, err := c.call(ctx, "getUserDetailsFirefly", []param{
		{"userID", siteUserID},
		{"alliance", "1"},
	})
	if err != nil {
		return nil, err
	}

	alliance, err := parseAlliance(raw)
	if err != nil {
		return nil, fmt.Errorf("альянс игрока %s: %w", siteUserID, err)
	}
	return alliance, nil
}

// parseAlliance разбирает ответ про игрока. Альянс приходит вложенным
// в properties, а у игрока без альянса поле просто null — это не ошибка,
// а полноценный ответ.
func parseAlliance(raw json.RawMessage) (*Alliance, error) {
	var res struct {
		Alliance *struct {
			Properties struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
				Tag  string `json:"tag"`
			} `json:"properties"`
		} `json:"alliance"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор: %w", err)
	}
	if res.Alliance == nil || res.Alliance.Properties.UID == "" {
		return nil, nil
	}
	p := res.Alliance.Properties
	return &Alliance{ID: p.UID, Name: p.Name, Tag: p.Tag}, nil
}

// AllianceMember — участник клана, как его отдаёт сайт: номер на сайте
// игры и ник. Больше про него из состава ничего не нужно — карту красит
// принадлежность к клану, а не заслуги.
type AllianceMember struct {
	SiteUserID string
	Name       string
}

// AllianceRoster забирает клан целиком: сам клан и весь его состав одним
// запросом. Это главный способ узнавать кланы: спрашивать про каждого
// игрока отдельно вышло бы во столько запросов, сколько в клане людей,
// а их до сорока.
func (c *Client) AllianceRoster(ctx context.Context, allianceID string) (*Alliance, []AllianceMember, error) {
	raw, err := c.call(ctx, "getAlliance", []param{
		{"allianceID", allianceID},
		{"members", "1"},
	})
	if err != nil {
		return nil, nil, err
	}
	return parseRoster(raw, allianceID)
}

// parseRoster разбирает ответ про клан. Состав приходит списком рядом
// с самим кланом, у каждого участника нас интересуют только номер и ник.
func parseRoster(raw json.RawMessage, allianceID string) (*Alliance, []AllianceMember, error) {
	var res struct {
		Properties struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
			Tag  string `json:"tag"`
		} `json:"properties"`
		Members []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("разбор клана %s: %w", allianceID, err)
	}
	if res.Properties.UID == "" {
		return nil, nil, fmt.Errorf("клан %s: сайт ответил без клана", allianceID)
	}

	alliance := &Alliance{
		ID: res.Properties.UID, Name: res.Properties.Name, Tag: res.Properties.Tag,
	}
	members := make([]AllianceMember, 0, len(res.Members))
	for _, m := range res.Members {
		if m.ID <= 0 {
			continue
		}
		members = append(members, AllianceMember{
			SiteUserID: strconv.FormatInt(m.ID, 10), Name: m.Username,
		})
	}
	return alliance, members, nil
}

// RankedAlliance — строка рейтинга кланов: сам клан и его место. Из всей
// статистики берём только очки: место и очки объясняют друг друга, а победы,
// поражения и средний счёт на карте не нужны ни в каком виде.
type RankedAlliance struct {
	Alliance
	Rank int
	Elo  int
}

// AllianceRanking забирает страницу рейтинга кланов. Нумерация страниц
// с нуля, на странице numEntries записей — так их считает сам клиент игры,
// откуда имя вызова и параметры и взяты.
//
// Заходом в партию это не считается: вопрос идёт на сайт. Рейтинг общий
// для всех доменов игры, поэтому спрашивать его можно откуда угодно.
func (c *Client) AllianceRanking(ctx context.Context, page, numEntries int) ([]RankedAlliance, error) {
	raw, err := c.call(ctx, "getAllianceRanking", []param{
		{"page", strconv.Itoa(page)},
		{"numEntries", strconv.Itoa(numEntries)},
	})
	if err != nil {
		return nil, err
	}
	return parseRanking(raw)
}

// parseRanking разбирает ответ рейтинга. Клан лежит там же, где и в других
// ответах про кланы, — в properties; место и очки едут рядом, в stats.
//
// Пустой ответ — не ошибка: так рейтинг сообщает, что страница за концом
// списка. Общего числа кланов он не отдаёт вовсе, и глубину списка узнать
// можно только листанием до пустой страницы.
func parseRanking(raw json.RawMessage) ([]RankedAlliance, error) {
	var res struct {
		Entries []struct {
			Properties struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
				Tag  string `json:"tag"`
			} `json:"properties"`
			Stats struct {
				Elo        int `json:"elo"`
				GlobalRank int `json:"globalRank"`
			} `json:"stats"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор рейтинга кланов: %w", err)
	}

	out := make([]RankedAlliance, 0, len(res.Entries))
	for _, e := range res.Entries {
		p := e.Properties
		if p.UID == "" {
			continue
		}
		out = append(out, RankedAlliance{
			Alliance: Alliance{ID: p.UID, Name: p.Name, Tag: p.Tag},
			Rank:     e.Stats.GlobalRank,
			Elo:      e.Stats.Elo,
		})
	}
	return out, nil
}

// allianceWorkers — сколько альянсов спрашиваем одновременно. Игроков
// в партии до сорока, а сайт отвечает не мгновенно: последовательный обход
// не уложился бы в терпение человека, открывшего страницу. Больше десятка
// параллельных запросов слать не хочется — это всё-таки чужой сайт.
const allianceWorkers = 8

// UserAlliances спрашивает альянсы сразу у пачки игроков. Ключ в ответе
// есть у каждого, кого удалось спросить; nil в значении означает «спросили,
// альянса нет». Ошибка возвращается первая из случившихся, но ответ при
// этом не пустой: одного упавшего запроса мало, чтобы отказать всей карте.
func (c *Client) UserAlliances(ctx context.Context, siteUserIDs []string) (map[string]*Alliance, error) {
	out := make(map[string]*Alliance, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return out, nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	ids := make(chan string)

	workers := min(allianceWorkers, len(siteUserIDs))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				alliance, err := c.UserAlliance(ctx, id)
				mu.Lock()
				if err != nil {
					if firstEr == nil {
						firstEr = err
					}
				} else {
					out[id] = alliance
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range siteUserIDs {
		ids <- id
	}
	close(ids)
	wg.Wait()

	return out, firstEr
}

// UserStats — боевой счёт игрока с сайта игры. Приходит тем же вызовом,
// что и клан, только другими полями, и так же не требует захода в партию.
//
// Defeated и Casualties считаются по боям с живыми игроками: сайт держит
// бои с компьютерными отдельным набором, и складывать их не стоит.
type UserStats struct {
	SiteUserID    string
	Level         int
	Defeated      int64
	Casualties    int64
	Games         int
	SoloWins      int
	CoalitionWins int
	OverallScore  int64
}

// UserStats спрашивает у сайта боевой счёт игрока. Работает по тому же
// номеру, что партия отдаёт как siteUserID, и по любому игроку, не только
// по нашему: сам клиент игры так же смотрит чужие профили.
//
// nil без ошибки означает, что сайт про такого ничего не рассказал:
// так отвечает про удалённые аккаунты.
func (c *Client) UserStats(ctx context.Context, siteUserID string) (*UserStats, error) {
	raw, err := c.call(ctx, "getUserDetailsFirefly", []param{
		{"userID", siteUserID},
		{"rankProgress", "1"},
		{"gameStats", "1"},
	})
	if err != nil {
		return nil, err
	}

	stats, err := parseUserStats(raw)
	if err != nil {
		return nil, fmt.Errorf("счёт игрока %s: %w", siteUserID, err)
	}
	if stats != nil {
		stats.SiteUserID = siteUserID
	}
	return stats, nil
}

// parseUserStats разбирает ответ про игрока. Бои лежат по типам юнитов —
// столько строк, сколько в игре видов войск, — и нам нужна только их сумма:
// «кд» считается по всей армии сразу, как и в самом клиенте игры.
func parseUserStats(raw json.RawMessage) (*UserStats, error) {
	var res struct {
		RankProgress *struct {
			Level        int   `json:"currentRankLevel"`
			OverallScore int64 `json:"overallScore"`
		} `json:"rankProgress"`
		GameStats *struct {
			// combatScores — бои с живыми; combatScoresAI лежит рядом
			// и намеренно не читается.
			CombatScores map[string]struct {
				Defeated int64 `json:"defeated"`
				Casualty int64 `json:"casualty"`
			} `json:"combatScores"`
			Totals struct {
				Games         int `json:"gameJoin"`
				SoloWins      int `json:"soloVictory"`
				CoalitionWins int `json:"coalitionVictory"`
			} `json:"gameStatsScore"`
		} `json:"gameStats"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("разбор: %w", err)
	}
	// Ни уровня, ни боёв — про такого сайт не рассказал ничего, и нули
	// записывать нечестно.
	if res.RankProgress == nil && res.GameStats == nil {
		return nil, nil
	}

	var out UserStats
	if r := res.RankProgress; r != nil {
		out.Level, out.OverallScore = r.Level, r.OverallScore
	}
	if g := res.GameStats; g != nil {
		for _, u := range g.CombatScores {
			out.Defeated += u.Defeated
			out.Casualties += u.Casualty
		}
		out.Games, out.SoloWins, out.CoalitionWins = g.Totals.Games, g.Totals.SoloWins, g.Totals.CoalitionWins
	}
	return &out, nil
}

// UserStatsBatch спрашивает счёт сразу у пачки игроков — тем же способом,
// что и кланы: по игроку за раз, но в несколько потоков. Ключ есть у
// каждого, кого удалось спросить; nil в значении означает «спросили,
// а сайт про него молчит».
func (c *Client) UserStatsBatch(ctx context.Context, siteUserIDs []string) (map[string]*UserStats, error) {
	out := make(map[string]*UserStats, len(siteUserIDs))
	if len(siteUserIDs) == 0 {
		return out, nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	ids := make(chan string)

	workers := min(allianceWorkers, len(siteUserIDs))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				stats, err := c.UserStats(ctx, id)
				mu.Lock()
				if err != nil {
					if firstEr == nil {
						firstEr = err
					}
				} else {
					out[id] = stats
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range siteUserIDs {
		ids <- id
	}
	close(ids)
	wg.Wait()

	return out, firstEr
}

type param struct{ key, value string }

// call вызывает действие API, при отказе игры один раз перелогинивается
// и повторяет. Сетевые ошибки не ретраим — их разберёт вызывающий тикер.
func (c *Client) call(ctx context.Context, action string, params []param) (json.RawMessage, error) {
	sess, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := c.callOnce(ctx, sess, action, params)
	if err == nil {
		return raw, nil
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.needsRelogin() {
		return nil, err
	}

	c.log.Warn("сессия протухла, перезаходим", "action", action, "err", err)
	c.invalidate(sess)
	sess, err = c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return c.callOnce(ctx, sess, action, params)
}

func (c *Client) callOnce(ctx context.Context, sess *session, action string, params []param) (json.RawMessage, error) {
	// authTstamp/authUserID/source клиент игры дописывает в конец — порядок
	// повторяем в точности, иначе подпись не сойдётся.
	all := make([]param, 0, len(params)+3)
	all = append(all, params...)
	all = append(all,
		param{"authTstamp", sess.authTstamp},
		param{"authUserID", sess.userID},
		param{"source", trackingSource},
	)

	var signed, encoded strings.Builder
	for i, p := range all {
		if i > 0 {
			signed.WriteByte('&')
			encoded.WriteByte('&')
		}
		signed.WriteString(p.key)
		signed.WriteByte('=')
		signed.WriteString(p.value)

		encoded.WriteString(encodeURIComponent(p.key))
		encoded.WriteByte('=')
		encoded.WriteString(encodeURIComponent(p.value))
	}

	sum := sha1.Sum([]byte(apiKey + action + signed.String() + sess.authHash))
	endpoint := fmt.Sprintf(
		"%s/index.php?action=%s&eID=api&key=%s&hash=%s&outputFormat=json&apiVersion=%s&L=0&source=%s",
		baseURL, action, apiKey, hex.EncodeToString(sum[:]), apiVersion, trackingSource,
	)
	body := "data=" + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(encoded.String())))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: чтение ответа: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Action: action, Status: resp.StatusCode}
	}

	var env struct {
		ResultCode    int             `json:"resultCode"`
		ResultMessage string          `json:"resultMessage"`
		Result        json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, &APIError{Action: action, Malformed: true, Message: err.Error()}
	}
	if env.ResultCode != 0 {
		return nil, &APIError{Action: action, Code: env.ResultCode, Message: env.ResultMessage}
	}
	return env.Result, nil
}

func (c *Client) ensureSession(ctx context.Context) (*session, error) {
	if sess := c.current(); sess != nil {
		return sess, nil
	}

	// Входит один, получают все: остальные ждут ту же попытку, а не заводят
	// свою. Ждут по-честному — со своим сроком: у страницы партии он свой,
	// и стоять за чужим входом дольше него ей незачем. Сам вход при этом
	// не обрывается: он нужен не одному ей.
	wait := c.startLogin()

	select {
	case <-wait:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.mu.Lock()
	sess, err := c.sess, c.attemptErr
	c.mu.Unlock()
	if sess != nil {
		return sess, nil
	}
	if err == nil {
		// Попытка кончилась ничем и без ошибки — так бывает, если подпись
		// успели сбросить прямо после удачного входа. Своей попытки заводить
		// не будем: следующий вызов начнёт всё заново.
		err = errors.New("подписи нет")
	}
	return nil, err
}

// startLogin отдаёт канал попытки: чужой, если кто-то уже входит, или свой,
// заведя новую. Канал закрывается, когда попытка кончилась — удачей или нет.
func (c *Client) startLogin() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loginDone != nil {
		return c.loginDone
	}
	done := make(chan struct{})
	c.loginDone = done
	go c.loginOnce(done)
	return done
}

// loginOnce — одна попытка добыть подпись: сперва прошлого запуска, потом
// входом. Идёт на своём сроке, а не на сроке того, кто её начал: страницу
// могли закрыть, а подпись нужна и воркерам, и следующему открывшему.
func (c *Client) loginOnce(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	var (
		sess *session
		err  error
	)
	defer func() {
		c.mu.Lock()
		c.attemptErr = err
		if sess != nil {
			c.sess = sess
		}
		c.loginDone = nil
		c.mu.Unlock()
		close(done)
	}()

	// Подпись с прошлого запуска обычно ещё жива: вход ради неё — лишний,
	// а лишний вход и есть то, за что сайт отказывает.
	if sess = c.restoreSession(ctx); sess != nil {
		return
	}

	// После отказа держим паузу: пока она идёт, зовущий получает ту же
	// ошибку, но без похода на сайт. Иначе полсотни вызовов воркера
	// превращаются в полсотни попыток войти.
	if err = c.loginPaused(); err != nil {
		return
	}

	if sess, err = c.login(ctx); err != nil {
		sess = nil
		c.noteLoginFail(err)
		return
	}
	c.noteLoginOK()
	c.saveSession(ctx, sess)
}

// restoreSession достаёт подпись прошлого запуска — один раз за процесс.
// Не подошла — узнаем об этом обычным путём: игра ответит «сессия
// протухла», и тогда мы войдём заново.
func (c *Client) restoreSession(ctx context.Context) *session {
	c.mu.Lock()
	store, done := c.sessions, c.restored
	c.restored = true
	c.mu.Unlock()
	if store == nil || done {
		return nil
	}

	saved, ok, err := store.LoadSession(ctx)
	if err != nil {
		c.log.Warn("подпись прошлого запуска не прочиталась", "err", err)
		return nil
	}
	if !ok || saved.AuthHash == "" || saved.UserID == "" {
		return nil
	}
	if age := time.Since(saved.SavedAt); age > sessionReuse {
		c.log.Info("подпись прошлого запуска слишком стара, входим заново",
			"возраст", age.Round(time.Minute))
		return nil
	}

	c.log.Info("подпись прошлого запуска подошла, вход не нужен", "userID", saved.UserID)
	return &session{userID: saved.UserID, authHash: saved.AuthHash, authTstamp: saved.AuthTstamp}
}

// saveSession кладёт свежую подпись на будущее. Не вышло — не беда:
// потеряем её только при перезапуске, и он же всё починит входом.
func (c *Client) saveSession(ctx context.Context, sess *session) {
	c.mu.Lock()
	store := c.sessions
	c.mu.Unlock()
	if store == nil {
		return
	}
	err := store.SaveSession(ctx, SavedSession{
		UserID: sess.userID, AuthHash: sess.authHash,
		AuthTstamp: sess.authTstamp, SavedAt: time.Now(),
	})
	if err != nil {
		c.log.Warn("подпись не сохранилась", "err", err)
	}
}

// loginPaused — идёт ли сейчас пауза после неудачи. Ошибку отдаём ту же,
// что и в прошлый раз: причина не изменилась, а «попробуем через 5 минут»
// объясняет, почему мы даже не пытались.
func (c *Client) loginPaused() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if left := time.Until(c.loginNextAt); left > 0 {
		return fmt.Errorf("%w ещё на %s после %d неудач подряд: %w",
			ErrLoginPaused, left.Round(time.Second), c.loginFails, c.loginErr)
	}
	return nil
}

func (c *Client) noteLoginFail(err error) {
	c.mu.Lock()
	c.loginFails++
	c.loginErr = err
	wait := loginBackoff[min(c.loginFails, len(loginBackoff))-1]
	c.loginNextAt = time.Now().Add(wait)
	fails := c.loginFails
	c.mu.Unlock()

	c.log.Warn("вход не удался, ждём перед следующей попыткой",
		"пауза", wait, "неудач подряд", fails, "err", err)
}

func (c *Client) noteLoginOK() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loginFails, c.loginErr, c.loginNextAt = 0, nil, time.Time{}
}

// UseSessionStore включает хранение подписи между запусками.
func (c *Client) UseSessionStore(store SessionStore) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = store
}

func (c *Client) current() *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// invalidate сбрасывает сессию, но только если её не подменили параллельно:
// иначе два одновременных отказа выбьют свежий логин соседа.
func (c *Client) invalidate(stale *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == stale {
		c.sess = nil
	}
}

var (
	reUberHash   = regexp.MustCompile(`uberAuthHash=([0-9a-f]{40})`)
	reUberTstamp = regexp.MustCompile(`uberAuthTstamp=(\d+)`)
	reUserID     = regexp.MustCompile(`userID=(\d+)`)
)

// login проходит форму входа и вытаскивает подписные данные из /game.php.
func (c *Client) login(ctx context.Context) (*session, error) {
	// Свежая банка на каждый логин: остатки прошлой сессии только мешают.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c.http.Jar = jar

	// Прогреваем страницу входа — на ней ставятся стартовые куки.
	if err := c.get(ctx, baseURL+"/index.php?id=188&re=103"); err != nil {
		return nil, fmt.Errorf("страница входа: %w", err)
	}

	form := url.Values{
		"user":      {c.user},
		"pass":      {c.pass},
		"logintype": {"login"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/index.php?id=188", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/index.php?id=188&re=103")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("вход: %w", err)
	}
	defer resp.Body.Close()

	page, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("вход: чтение ответа: %w", err)
	}
	// Удачный вход уводит редиректом на game.php; всё остальное — отказ.
	if !strings.Contains(resp.Request.URL.Path, "/game.php") {
		return nil, fmt.Errorf("вход не удался: остались на %s (проверьте S1914_USER и S1914_PASSWORD)",
			resp.Request.URL.Path)
	}

	html := string(page)
	hash := firstSubmatch(reUberHash, html)
	tstamp := firstSubmatch(reUberTstamp, html)
	userID := firstSubmatch(reUserID, html)
	if hash == "" || tstamp == "" || userID == "" {
		return nil, fmt.Errorf("в game.php не нашлись данные подписи (uberAuthHash=%q uberAuthTstamp=%q userID=%q)",
			hash, tstamp, userID)
	}

	c.log.Info("вход в игру выполнен", "userID", userID)
	return &session{userID: userID, authHash: hash, authTstamp: tstamp}, nil
}

func (c *Client) get(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	return err
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// encodeURIComponent повторяет одноимённую функцию JS, которой клиент игры
// собирает тело запроса. url.QueryEscape здесь не годится: он кодирует
// пробел как «+» и экранирует ! ' ( ) * ~, а JS их оставляет.
func encodeURIComponent(s string) string {
	const safe = "-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case 'A' <= ch && ch <= 'Z', 'a' <= ch && ch <= 'z', '0' <= ch && ch <= '9',
			strings.IndexByte(safe, ch) >= 0:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}
