package web

import "net/http"

// faq — справка внутри админки. Статична: вопросы одни и те же для всех,
// а часть ответов шаблон показывает только админам.
func (s *Server) faq(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "faq", nil)
}

// privacy — политика конфиденциальности расширения Chrome. Публичная:
// ссылку на неё открывают проверяющие магазина, у них входа нет.
func (s *Server) privacy(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "privacy", nil)
}
