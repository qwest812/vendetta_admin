package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Большую страницу сжимаем, и распакованная она совпадает с исходной
// байт в байт; длина несжатого тела из заголовков уходит.
func TestCompressPage(t *testing.T) {
	body := strings.Repeat(`<polygon points="1,2 3,4 5,6" class="clan"></polygon>`, 2000)
	h := compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", "999")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, body)
	}))

	req := httptest.NewRequest(http.MethodGet, "/games/1", nil)
	req.Header.Set("Accept-Encoding", "br, gzip, deflate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("код = %d, ожидался 403: сжатие не должно менять код", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, ожидался gzip", got)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("длина несжатого тела осталась в заголовках")
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Error("нет Vary: Accept-Encoding")
	}
	if rec.Body.Len() >= len(body)/5 {
		t.Errorf("сжато плохо: %d из %d байт", rec.Body.Len(), len(body))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("не gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("распаковка: %v", err)
	}
	if !bytes.Equal(got, []byte(body)) {
		t.Error("распакованное тело не совпало с исходным")
	}
}

// Где сжатие не нужно или вредно, ответ уходит как есть.
func TestCompressSkips(t *testing.T) {
	big := strings.Repeat("a", 4096)
	cases := []struct {
		name     string
		accept   string
		rangeHdr string
		ctype    string
		body     string
	}{
		{name: "браузер не умеет", accept: "", ctype: "text/html", body: big},
		{name: "браузер отказался", accept: "gzip;q=0", ctype: "text/html", body: big},
		{name: "маленький ответ", accept: "gzip", ctype: "text/html", body: "<p>ok</p>"},
		{name: "картинка", accept: "gzip", ctype: "image/png", body: big},
		{name: "кусок файла", accept: "gzip", rangeHdr: "bytes=0-10", ctype: "text/css", body: big},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.ctype)
				_, _ = io.WriteString(w, c.body)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.accept != "" {
				req.Header.Set("Accept-Encoding", c.accept)
			}
			if c.rangeHdr != "" {
				req.Header.Set("Range", c.rangeHdr)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Header().Get("Content-Encoding") != "" {
				t.Error("ответ сжат, а не должен был")
			}
			if rec.Body.String() != c.body {
				t.Error("тело изменилось")
			}
		})
	}
}

// Ответ без тела — редирект после формы — должен уйти со своим кодом,
// хоть решать про сжатие было и не по чему.
func TestCompressKeepsBodilessStatus(t *testing.T) {
	h := compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/games", http.StatusSeeOther)
	}))
	req := httptest.NewRequest(http.MethodPost, "/games/1/hero", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/games" {
		t.Errorf("код = %d, Location = %q", rec.Code, rec.Header().Get("Location"))
	}
}
