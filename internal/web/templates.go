package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

var funcs = template.FuncMap{
	"datetime": func(t time.Time) string { return t.Local().Format("02.01.2006 15:04") },
	"dict": func(values ...any) (map[string]any, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict: нечётное число аргументов")
		}
		m := make(map[string]any, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict: ключ должен быть строкой")
			}
			m[key] = values[i+1]
		}
		return m, nil
	},
}

// pages — по одному дереву шаблонов на страницу: каждая страница
// подмешивается к общему каркасу base.gohtml.
type pages map[string]*template.Template

func parseTemplates() (pages, error) {
	names, err := fs.Glob(templatesFS, "templates/*.gohtml")
	if err != nil {
		return nil, err
	}

	out := pages{}
	for _, name := range names {
		base := strings.TrimSuffix(name[len("templates/"):], ".gohtml")
		if base == "base" || strings.HasPrefix(base, "_") {
			continue
		}
		files := []string{"templates/base.gohtml", name}
		if partials, _ := fs.Glob(templatesFS, "templates/_*.gohtml"); len(partials) > 0 {
			files = append(files, partials...)
		}
		tmpl, err := template.New("base.gohtml").Funcs(funcs).ParseFS(templatesFS, files...)
		if err != nil {
			return nil, fmt.Errorf("шаблон %s: %w", name, err)
		}
		out[base] = tmpl
	}
	return out, nil
}

// render буферизует вывод, чтобы ошибка шаблона не отдавалась
// пользователю посреди наполовину сформированной страницы.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("нет шаблона %q", page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["CurrentUser"] = currentUser(r)
	data["CSRFToken"] = csrfToken(r)
	data["Path"] = r.URL.Path

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base.gohtml", data); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// renderPartial отдаёт один блок шаблона — для ответов HTMX.
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, page, block string, data map[string]any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("нет шаблона %q", page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["CurrentUser"] = currentUser(r)
	data["CSRFToken"] = csrfToken(r)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, block, data); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func csrfToken(r *http.Request) string {
	if sess := currentSession(r); sess != nil {
		return sess.CSRFToken
	}
	return ""
}
