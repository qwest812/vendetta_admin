package supremacy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

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
