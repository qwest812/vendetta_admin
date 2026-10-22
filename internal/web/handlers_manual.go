package web

import (
	"context"
	"net/http"

	"Vendetta_admin/internal/repo"
)

// manualOn — открыты ли ручные разделы: признаки, комментарии, герои,
// друзья и враги, ручные карточки. Без настроек в базе рубильнику негде
// жить, и тогда всё открыто, как было до него. Ошибка чтения тоже
// оставляет разделы открытыми: из-за одной запинки базы прятать у людей
// их записи хуже, чем показать их лишний раз.
func (s *Server) manualOn(ctx context.Context) bool {
	if s.settings == nil {
		return true
	}
	on, err := s.settings.ManualDataEnabled(ctx)
	if err != nil {
		s.log.Warn("не прочитался рубильник ручных разделов", "err", err)
		return true
	}
	return on
}

// requireManual закрывает ручной раздел, пока рубильник выключен. Мало
// спрятать его в интерфейсе: открытая заранее вкладка и прямая ссылка
// иначе продолжали бы писать. Отвечает 404, а не 403: раздела сейчас
// просто нет, и права тут ни при чём.
func (s *Server) requireManual(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.manualOn(r.Context()) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// manualDataToggle — рубильник ручных разделов. Рутовый, с записью
// в журнал, как и соседние; данные он не трогает, только прячет.
func (s *Server) manualDataToggle(w http.ResponseWriter, r *http.Request) {
	on := r.PostFormValue("on") == "1"
	me := currentUser(r)

	if err := s.settings.SetBool(r.Context(), repo.SettingManualData, on, me.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("ручные разделы переключены", "включены", on, "user_id", me.ID)
	s.logAuditOn(r, "manual_data", "settings", 0, map[string]any{"on": on})

	// Целиком страницей, а не карточкой: вместе с рубильником появляется
	// или пропадает справочник признаков, и меню наверху меняется тоже.
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
