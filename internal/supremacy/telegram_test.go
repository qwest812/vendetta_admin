package supremacy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestNotifier подсовывает отправителю локальный сервер вместо телеграма.
func newTestNotifier(srv *httptest.Server) *TelegramNotifier {
	return newTestTopicNotifier(srv, 0)
}

const testUserID = "101408369"

func newTestTopicNotifier(srv *httptest.Server, topicID int) *TelegramNotifier {
	n := NewTelegramNotifier("123:secret", "-1001", topicID, func() string { return testUserID })
	n.base = srv.URL
	n.http = srv.Client()
	return n
}

// catchBody поднимает сервер, который принимает всё подряд и запоминает тело.
func catchBody(t *testing.T, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, got); err != nil {
			t.Errorf("тело запроса не разобралось: %v", err)
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
}

// В группе-форуме без message_thread_id сообщение улетит в общую тему.
func TestTelegramNotifySendsToTopic(t *testing.T) {
	var body map[string]any
	srv := catchBody(t, &body)
	defer srv.Close()

	if err := newTestTopicNotifier(srv, 42).Notify(context.Background(), Game{GameID: "1", Title: "X"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if body["message_thread_id"] != float64(42) {
		t.Errorf("message_thread_id = %v, ожидалось 42", body["message_thread_id"])
	}
}

// А в обычном чате тем нет, и телеграм отвергает запрос с указанием темы —
// поэтому поля не должно быть вовсе, а не «ноль».
func TestTelegramNotifyOmitsTopicWhenUnset(t *testing.T) {
	var body map[string]any
	srv := catchBody(t, &body)
	defer srv.Close()

	if err := newTestNotifier(srv).Notify(context.Background(), Game{GameID: "1", Title: "X"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if _, ok := body["message_thread_id"]; ok {
		t.Errorf("в запрос попала тема, хотя её не задавали: %v", body["message_thread_id"])
	}
}

func TestTelegramNotifySendsMessage(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("тело запроса не разобралось: %v", err)
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer srv.Close()

	g := Game{GameID: "4123", Title: "The Great War", OpenSlots: "3", NrOfPlayers: "27", DayOfGame: "1"}
	if err := newTestNotifier(srv).Notify(context.Background(), g); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if want := "/bot123:secret/sendMessage"; gotPath != want {
		t.Errorf("запрос ушёл на %s, ожидалось %s", gotPath, want)
	}
	if gotBody["chat_id"] != "-1001" {
		t.Errorf("chat_id = %v, ожидалось -1001", gotBody["chat_id"])
	}
	if gotBody["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, ожидался HTML", gotBody["parse_mode"])
	}

	text, _ := gotBody["text"].(string)
	// В ссылке ожидаем аккаунт воркера, а id найденной игры — отдельно
	// текстом: по ссылке открывается игра вообще, а не эта партия.
	for _, want := range []string{
		"The Great War", "свободно 3", "игроков 27", "день 1",
		"https://www.supremacy1914.com/game.php?bust=1&amp;uid=" + testUserID, "id 4123",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в сообщении нет %q:\n%s", want, text)
		}
	}
}

// Отказ телеграма приходит с ok=false и http 200 далеко не всегда — важно,
// что воркер увидит ошибку и повторит на следующем тике.
func TestTelegramNotifyReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()

	err := newTestNotifier(srv).Notify(context.Background(), Game{GameID: "1", Title: "X"})
	if err == nil {
		t.Fatal("ожидалась ошибка, получили nil")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("в ошибке нет описания от телеграма: %v", err)
	}
}

func TestTelegramNotifyReportsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>502 Bad Gateway</html>")
	}))
	defer srv.Close()

	err := newTestNotifier(srv).Notify(context.Background(), Game{GameID: "1", Title: "X"})
	if err == nil {
		t.Fatal("ожидалась ошибка, получили nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("в ошибке нет http-статуса: %v", err)
	}
}

// Ошибки уходят в лог, а адрес запроса содержит токен бота — его там быть
// не должно.
func TestTelegramNotifyHidesTokenFromError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // соединиться уже не с кем — получим сетевую ошибку

	err := newTestNotifier(srv).Notify(context.Background(), Game{GameID: "1", Title: "X"})
	if err == nil {
		t.Fatal("ожидалась ошибка, получили nil")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("токен утёк в текст ошибки: %v", err)
	}
}

// Название игры приходит от других игроков, так что разметку из него надо
// обезвредить — иначе телеграм отклонит сообщение целиком.
func TestTelegramTextEscapesMarkup(t *testing.T) {
	text := telegramText(Game{GameID: "1", Title: `<b>Fake</b> & "co"`}, PlayURL(testUserID))
	if !strings.Contains(text, "&lt;b&gt;Fake&lt;/b&gt; &amp; \"co\"") {
		t.Errorf("разметка в названии не обезврежена:\n%s", text)
	}
}

// Пустые поля игра отдаёт нередко; сообщение не должно превращаться в
// «свободно  · игроков  · день».
func TestTelegramTextSkipsEmptyFields(t *testing.T) {
	text := telegramText(Game{GameID: "7", Title: "Mesopotamia", OpenSlots: "5"}, PlayURL(testUserID))
	if !strings.Contains(text, "свободно 5") {
		t.Errorf("нет числа слотов:\n%s", text)
	}
	for _, unwanted := range []string{"игроков", "день"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("в сообщении оказалось пустое поле %q:\n%s", unwanted, text)
		}
	}
}
