package supremacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const telegramAPI = "https://api.telegram.org"

// TelegramNotifier рассказывает о найденных играх в чат телеграма.
//
// Ретраев внутри нет намеренно: воркер не помечает игру увиденной, если
// уведомление не ушло, так что повтор случится сам на следующем тике.
type TelegramNotifier struct {
	http    *http.Client
	base    string // адрес API; в тестах подменяется на httptest
	token   string
	chatID  string
	topicID int
	userID  func() string
}

// NewTelegramNotifier готовит отправителя. chatID — это либо число (в том
// числе отрицательное у групп), либо «@username» публичного канала.
//
// topicID нужен только для групп-форумов и задаёт тему, в которую писать;
// ноль означает «обычный чат» — тогда поле в запрос не попадает вовсе, иначе
// телеграм ответит, что темы тут нет.
//
// userID отдаёт аккаунт для ссылки. Это функция, а не строка, потому что
// отправитель создаётся до логина в игру, а userID известен только после.
// nil допустим — ссылка тогда будет без uid.
func NewTelegramNotifier(token, chatID string, topicID int, userID func() string) *TelegramNotifier {
	if userID == nil {
		userID = func() string { return "" }
	}
	return &TelegramNotifier{
		http:    &http.Client{Timeout: 15 * time.Second},
		base:    telegramAPI,
		token:   token,
		chatID:  chatID,
		topicID: topicID,
		userID:  userID,
	}
}

func (t *TelegramNotifier) Notify(ctx context.Context, g Game) error {
	msg := map[string]any{
		"chat_id":    t.chatID,
		"text":       telegramText(g, PlayURL(t.userID())),
		"parse_mode": "HTML",
		// Превью ни к чему: по ссылке незалогиненному покажут страницу входа.
		"link_preview_options": map[string]bool{"is_disabled": true},
	}
	if t.topicID != 0 {
		msg["message_thread_id"] = t.topicID
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.base+"/bot"+t.token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("телеграм: %w", hideToken(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("телеграм: %w", hideToken(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("телеграм: чтение ответа: %w", hideToken(err))
	}

	// Отказ телеграм тоже отдаёт JSON'ом, а вот на 5xx может прийти HTML.
	var res struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("телеграм: http %d, ответ не разобрался как JSON", resp.StatusCode)
	}
	if !res.OK {
		return fmt.Errorf("телеграм: error_code=%d %s", res.ErrorCode, res.Description)
	}
	return nil
}

// telegramText собирает сообщение. Разметка — HTML, а не MarkdownV2: в
// названиях игр сплошь скобки и дефисы, которые в MarkdownV2 пришлось бы
// экранировать поштучно.
func telegramText(g Game, link string) string {
	var b strings.Builder
	b.WriteString("🎮 <b>")
	b.WriteString(escapeHTML(g.Title))
	b.WriteString("</b>\n")

	// Числа игра отдаёт строками и на некоторых играх часть полей пустая —
	// показываем только то, что реально пришло.
	var parts []string
	if g.OpenSlots != "" {
		parts = append(parts, "свободно "+escapeHTML(g.OpenSlots))
	}
	if g.NrOfPlayers != "" {
		parts = append(parts, "игроков "+escapeHTML(g.NrOfPlayers))
	}
	if g.DayOfGame != "" {
		parts = append(parts, "день "+escapeHTML(g.DayOfGame))
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, " · "))
		b.WriteByte('\n')
	}

	// Ссылка ведёт в игру вообще, а не в найденную партию, поэтому id рядом
	// обязателен: по нему её отыскивают в списке.
	fmt.Fprintf(&b, `<a href="%s">открыть</a> · id %s`, escapeHTML(link), escapeHTML(g.GameID))
	return b.String()
}

// escapeHTML экранирует то и только то, что требует режим HTML у телеграма.
// html.EscapeString здесь не годится: он превращает кавычки и апострофы в
// числовые сущности, а их телеграм оставляет в тексте как есть.
var escapeHTML = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// hideToken убирает из ошибки адрес запроса: в нём токен бота, а ошибка
// уходит в лог.
func hideToken(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}
