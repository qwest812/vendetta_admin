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

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// funcsFor — функции шаблона для одного языка. Язык здесь замыкается,
// а не передаётся аргументом: наборов шаблонов у нас по одному на язык,
// поэтому в самих шаблонах достаточно {{t "ключ"}} — и внутри range,
// и внутри вложенных шаблонов, где «точка» уже не та.
func funcsFor(l i18n.Lang) template.FuncMap {
	return template.FuncMap{
		"t":    l.T,
		"lang": func() i18n.Lang { return l },
		// otherLangs — что показать в переключателе: все языки, кроме
		// текущего. Их пока два, но пусть шаблон не знает и об этом.
		"otherLangs": func() []i18n.Lang { return otherLangs(l) },
		// Формат даты — часть перевода: русский и английский пишут её
		// по-разному, и подставлять один в оба было бы небрежностью.
		"datetime": func(t time.Time) string { return t.Local().Format(l.T("format.datetime")) },
		// Дата без времени: в счёте по дням час ни к чему.
		"date": func(t time.Time) string { return t.Local().Format(l.T("format.date")) },
		// Кд и опасность печатаются с двумя знаками: числа маленькие,
		// и разница между 1.2 и 1.25 в них существенна.
		"ratio": formatRatio,
		// Интервал воркера в интерфейсе читается словами: «45 мин» вместо «45m0s».
		"every": func(d time.Duration) string { return everyText(l, d) },
		// Разброс интервала пишется одной подписью — «45–50 мин», а не
		// «45 мин – 50 мин»: единица у обеих границ одна и та же.
		// Равные границы означают ровный интервал, и тире там ни к чему.
		"everySpan": func(from, to time.Duration) string {
			if to <= from {
				return everyText(l, from)
			}
			if fh, th := int(from.Hours()), int(to.Hours()); fh > 0 && from%time.Hour == 0 && to%time.Hour == 0 {
				return l.T("unit.hours.span", fh, th)
			}
			return l.T("unit.minutes.span", int(from.Minutes()), int(to.Minutes()))
		},
		// Роль, статус клана и уровень шкалы приходят из domain кодами:
		// подпись к коду — дело языка, а не предметной области.
		"role":       func(r domain.Role) string { return l.T(r.TitleKey()) },
		"clanStatus": func(c domain.ClanStatus) string { return l.T(c.TitleKey()) },
		// Список имён в одну строку: подсказка «кто отметил» показывается
		// атрибутом title, а он строку и ждёт.
		"join": func(items []string) string { return strings.Join(items, ", ") },
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
}

// pages — по одному дереву шаблонов на страницу: каждая страница
// подмешивается к общему каркасу base.gohtml.
type pages map[string]*template.Template

// site — шаблоны всех страниц на всех языках. Разбор один и тот же, разные
// только функции: дешевле держать по набору на язык, чем таскать язык через
// каждую «точку» в шаблонах.
type site map[i18n.Lang]pages

func parseSite() (site, error) {
	out := site{}
	for _, l := range i18n.All {
		p, err := parseTemplates(l)
		if err != nil {
			return nil, fmt.Errorf("язык %s: %w", l, err)
		}
		out[l] = p
	}
	return out, nil
}

func parseTemplates(l i18n.Lang) (pages, error) {
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
		tmpl, err := template.New("base.gohtml").Funcs(funcsFor(l)).ParseFS(templatesFS, files...)
		if err != nil {
			return nil, fmt.Errorf("шаблон %s: %w", name, err)
		}
		out[base] = tmpl
	}
	return out, nil
}

// everyText — интервал словами: «45 мин», «2 ч». Час пишется часом только
// когда он целый, иначе счёт идёт в минутах.
func everyText(l i18n.Lang, d time.Duration) string {
	if h := int(d.Hours()); h > 0 && d%time.Hour == 0 {
		return l.T("unit.hours", h)
	}
	return l.T("unit.minutes", int(d.Minutes()))
}

// render буферизует вывод, чтобы ошибка шаблона не отдавалась
// пользователю посреди наполовину сформированной страницы.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	lang := langOf(r)
	tmpl, ok := s.pages[lang][page]
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
	data["FeedbackBadge"] = s.feedbackBadge(r)
	data["Lang"] = lang
	// Back — куда вернуться после переключения языка: на ту же страницу
	// со всеми её параметрами, а не на главную.
	data["Back"] = r.URL.RequestURI()

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
	s.renderPartialStatus(w, r, http.StatusOK, page, block, data)
}

// renderPartialStatus — тот же блок, но со своим кодом ответа. Нужен там,
// где htmx отдаёт не «получилось», а «так нельзя»: код должен остаться
// честным, а разметка всё равно приезжает — в ней и написано, что не так.
func (s *Server) renderPartialStatus(w http.ResponseWriter, r *http.Request, status int,
	page, block string, data map[string]any) {

	tmpl, ok := s.pages[langOf(r)][page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("нет шаблона %q", page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["CurrentUser"] = currentUser(r)
	data["CSRFToken"] = csrfToken(r)
	data["Lang"] = langOf(r)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, block, data); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// feedbackBadge — число рядом с разделом обратной связи в шапке. Считается
// на каждой странице: пометка «вам ответили» бесполезна, если её видно только
// в самом разделе. Ошибку показывать некому и незачем — шапка не то место,
// где сообщают о сбое базы, поэтому она просто уходит в лог.
func (s *Server) feedbackBadge(r *http.Request) int {
	me := currentUser(r)
	if me == nil || s.feedback == nil {
		return 0
	}
	n, err := s.feedback.Badge(r.Context(), me)
	if err != nil {
		s.log.Error("не посчитаны обращения", "err", err, "user_id", me.ID)
		return 0
	}
	return n
}

func csrfToken(r *http.Request) string {
	if sess := currentSession(r); sess != nil {
		return sess.CSRFToken
	}
	return ""
}
