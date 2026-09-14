package web

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// Сжатие ответов. Страница большой партии весит около трёх мегабайт —
// почти всё это очертания провинций, — и при первом открытии уходит дважды:
// сначала карта, потом она же с составом. В gzip это впятеро меньше, а
// перед приложением никакого прокси, который сжал бы сам, нет.

// compressMin — меньше этого сжимать незачем: заголовки и словарь gzip
// съедят выигрыш, а таких ответов — htmx-кусочков и редиректов — большинство.
const compressMin = 1024

// gzipPool — писатели gzip держат внутри словарь на сотни килобайт, и
// заводить его на каждый ответ — лишняя работа сборщику мусора.
var gzipPool = sync.Pool{New: func() any {
	// Уровень по умолчанию: трёхмегабайтную карту он жмёт за десятки
	// миллисекунд, а самый быстрый даёт заметно больший файл.
	w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return w
}}

// compressible — стоит ли сжимать ответ такого типа. Картинки (портреты
// героев) уже сжаты своим форматом, и второй раз их жать — пустая работа.
func compressible(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/javascript", ct == "application/json", ct == "image/svg+xml":
		return true
	}
	return false
}

// compress сжимает ответ, если браузер это умеет. Решение принимается
// на первой записи тела: только тогда известны и тип, и размер. Запросы
// с Range не трогаются — файловый сервер отдаёт на них кусок файла, и сжатый
// кусок с его границами уже не сойдётся.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" || !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &gzipResponse{ResponseWriter: w}
		defer cw.finish()
		next.ServeHTTP(cw, r)
	})
}

// acceptsGzip — назвал ли браузер gzip среди понятных ему кодировок.
// «gzip;q=0» означает отказ, и его стоит уважать.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// gzipResponse откладывает заголовок до первой записи тела и там решает,
// сжимать ли. status — отложенный код; decided — решение уже принято;
// gz — писатель, если решили сжимать.
type gzipResponse struct {
	http.ResponseWriter
	status  int
	decided bool
	gz      *gzip.Writer
}

func (g *gzipResponse) WriteHeader(status int) {
	if g.decided || g.status != 0 {
		return
	}
	// Промежуточные ответы (1xx) заголовок не закрывают — их пропускаем
	// сразу, решать по ним нечего.
	if status < 200 {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.status = status
}

func (g *gzipResponse) Write(p []byte) (int, error) {
	if !g.decided {
		g.decide(len(p))
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// decide смотрит на то, что обработчик успел сказать о теле, и отпускает
// заголовок. size — размер первой записи: наши страницы пишутся одним
// куском из буфера, а файловый сервер — кусками по 32 КБ, так что по первой
// записи о размере судить можно.
func (g *gzipResponse) decide(size int) {
	g.decided = true
	status := g.status
	if status == 0 {
		status = http.StatusOK
	}

	h := g.Header()
	if ct := h.Get("Content-Type"); ct != "" && compressible(ct) {
		// Ответ зависит от Accept-Encoding, и промежуточный кэш должен это
		// знать — даже когда именно этот ответ мы решили не сжимать.
		h.Add("Vary", "Accept-Encoding")
		if size >= compressMin && h.Get("Content-Encoding") == "" &&
			status != http.StatusNoContent && status != http.StatusNotModified &&
			status != http.StatusPartialContent {

			h.Set("Content-Encoding", "gzip")
			// Длина была про несжатое тело и теперь врёт.
			h.Del("Content-Length")
			g.gz = gzipPool.Get().(*gzip.Writer)
			g.gz.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(status)
}

// finish дописывает хвост gzip и возвращает писателя в пул. Если тела
// не было вовсе, отпускает отложенный заголовок как есть.
func (g *gzipResponse) finish() {
	if !g.decided {
		if g.status != 0 {
			g.ResponseWriter.WriteHeader(g.status)
		}
		return
	}
	if g.gz != nil {
		_ = g.gz.Close()
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}

// Unwrap даёт http.ResponseController дотянуться до настоящего писателя:
// сроки записи и прочее он ищет по цепочке.
func (g *gzipResponse) Unwrap() http.ResponseWriter { return g.ResponseWriter }
