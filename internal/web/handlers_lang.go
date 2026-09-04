package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
)

// langCookie — где хранится выбранный язык. В куке, а не в профиле:
// переключатель нужен и на странице входа, где пользователя ещё нет,
// и язык — свойство браузера, а не учётной записи: с чужого компьютера
// человек может захотеть другой.
const langCookie = "lang"

// langMaxAge — год. Выбор языка не из тех, что стоит переспрашивать.
const langMaxAge = 365 * 24 * time.Hour

// langOf — язык этого запроса. Незнакомое значение куки молча означает
// язык по умолчанию: протухшая кука не повод ломать страницу.
func langOf(r *http.Request) i18n.Lang {
	c, err := r.Cookie(langCookie)
	if err != nil {
		return i18n.Default
	}
	return i18n.Parse(c.Value)
}

// setLang — переключатель языка из шапки. Метод GET здесь уместен: кука
// с языком — это не изменение данных, а настройка показа, и ссылка работает
// там, где формы с csrf-токеном ещё нет, — на странице входа.
func (s *Server) setLang(w http.ResponseWriter, r *http.Request) {
	lang := r.PathValue("lang")
	if !i18n.Valid(lang) {
		http.Error(w, "неизвестный язык", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     langCookie,
		Value:    lang,
		Path:     "/",
		Expires:  time.Now().Add(langMaxAge),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, safeBack(r.URL.Query().Get("back")), http.StatusSeeOther)
}

// safeBack — куда вернуть человека после переключения. Адрес приходит
// из ссылки, поэтому чужой сайт в нём допускать нельзя: берём только путь
// внутри админки, всё остальное — на главную.
func safeBack(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		return "/"
	}
	// Обратный слеш браузеры разбирают как прямой, и «/\evil.com» уводит
	// наружу так же, как «//evil.com».
	if strings.HasPrefix(back, "/\\") {
		return "/"
	}
	return back
}

// otherLangs — языки, кроме текущего: ровно то, что показывает переключатель.
func otherLangs(cur i18n.Lang) []i18n.Lang {
	out := make([]i18n.Lang, 0, len(i18n.All)-1)
	for _, l := range i18n.All {
		if l != cur {
			out = append(out, l)
		}
	}
	return out
}

// errText — что показать человеку вместо ошибки. Ошибки предметной области
// носят ключ сообщения, и его надо перевести; всё остальное показываем как
// есть: это уже не наш текст, а ответ базы или сайта игры.
func errText(r *http.Request, err error) string {
	var m *domain.MsgError
	if errors.As(err, &m) {
		return langOf(r).T(m.Key)
	}
	return err.Error()
}
