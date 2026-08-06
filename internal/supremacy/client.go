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
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
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
}

// session — то, что даёт странице право подписывать вызовы API.
type session struct {
	userID     string
	authHash   string
	authTstamp string
}

// Коды ответа игры; взяты из таблицы ResultCode в клиентском app.js.
// Перечислены только те, что нам важны: первые два лечатся перезаходом,
// остальные — нет, и повторять запрос бессмысленно.
const (
	codeSessionExpired       = -17
	codeAuthenticationFailed = -18
)

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
		http: &http.Client{Timeout: 30 * time.Second},
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
}

// URL — ссылка, по которой игру видно в браузере.
func (g Game) URL() string {
	return fmt.Sprintf("%s/game.php?bust=1#c%s", baseURL, g.GameID)
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
		}
		// Страница пришла неполной или мы уже собрали всё обещанное —
		// дальше листать нечего.
		if len(res.Games) < perPage || len(out) >= res.NumGames {
			return out, nil
		}
	}
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
	c.mu.Lock()
	if c.sess != nil {
		sess := c.sess
		c.mu.Unlock()
		return sess, nil
	}
	c.mu.Unlock()

	sess, err := c.login(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.sess = sess
	c.mu.Unlock()
	return sess, nil
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
