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
	"net/url"
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

	// heroCards — карточки героев с сайта и когда их взяли, см. HeroCards.
	heroCards   map[int]HeroCard
	heroCardsAt time.Time
	// games — ответы сайта о партиях: сведения и состав. Живут недолго,
	// см. rosterTTL.
	games map[string]*gameEntry
	// states — состояния партий с игрового сервера: копия одна на всех,
	// кто бы её ни привёз. Подробности в state_cache.go.
	states map[string]*stateEntry
	// papers — разобранные дни газеты по партиям: сводки о войнах.
	// Подробности в losses.go.
	papers map[string]*paperEntry
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

// httpTimeout — предел на один запрос к сайту игры, и это именно предел,
// а не срок: сроки у зовущих свои и приходят контекстом. Держать его надо
// выше самого длинного из них (страница большой партии ждёт минуту),
// иначе он рубит раньше и вместо честного ожидания выходит обман: страница
// говорит «не ответил за минуту», а ждала на самом деле тридцать секунд.
const httpTimeout = 90 * time.Second

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
