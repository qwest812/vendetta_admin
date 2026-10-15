package supremacy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Заминка прокси игры (502) переживается повтором, а настоящая ошибка
// сервера (500) — нет: её повтор не вылечит.
func TestPaperDayRetriesGateway(t *testing.T) {
	saved := paperRetry
	paperRetry = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { paperRetry = saved }()

	const paper = `{"result":{"states":{"2":{"articles":["java.util.ArrayList",[]]}}}}`
	cases := []struct {
		name   string
		fails  int32
		status int
		ok     bool
		calls  int32
	}{
		{"два 502 и ответ", 2, http.StatusBadGateway, true, 3},
		{"502 без конца", 100, http.StatusBadGateway, false, 3},
		{"500 не повторяем", 100, http.StatusInternalServerError, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) <= c.fails {
					w.WriteHeader(c.status)
					return
				}
				_, _ = w.Write([]byte(paper))
			}))
			defer srv.Close()

			client := NewClient("u", "p", "ru", quietLog())
			client.http = srv.Client()
			acc := &gameAccess{host: srv.Listener.Addr().String(), auth: "a", tstamp: "1"}

			_, err := client.paperDay(context.Background(), acc, "1", 3)
			if (err == nil) != c.ok {
				t.Errorf("ошибка = %v, ожидался успех: %v", err, c.ok)
			}
			if got := calls.Load(); got != c.calls {
				t.Errorf("запросов = %d, ожидалось %d", got, c.calls)
			}
		})
	}
}
