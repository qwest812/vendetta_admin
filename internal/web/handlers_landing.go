package web

import (
	"net/http"
	"time"
)

// landingWeek — окно «за неделю» на витрине.
const landingWeek = 7 * 24 * time.Hour

// index — корень сайта. Вошедший попадает в поиск, как и раньше, а гость
// видит витрину: что это за админка и как в неё попасть. Раньше гостя
// отсюда сразу уводило на вход, и по адресу было не понять, куда пришёл.
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		s.overview(w, r)
		return
	}
	s.landing(w, r)
}

// landing — витрина для гостя. Цифры на ней настоящие и растут сами,
// без участия людей: карточки заводятся из партий, а партии находит
// воркер в лобби. Не посчиталась цифра — нет плашки: открытый всем адрес
// не должен отвечать 500 из-за того, что база задумалась.
func (s *Server) landing(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Landing": true}

	if s.players != nil {
		sc, err := s.players.Showcase(r.Context(), time.Now().Add(-landingWeek))
		if err == nil {
			data["Players"] = sc.Players
			data["PlayersWeek"] = sc.Recent
			data["Renamed"] = sc.Renamed
			data["Banned"] = sc.Banned
		} else {
			s.log.Warn("витрина: не посчитались игроки", "err", err)
		}
	}
	if s.coalitions != nil {
		if st, err := s.coalitions.Stats(r.Context()); err == nil {
			data["Games"] = st.Watched
		} else {
			s.log.Warn("витрина: не посчитались партии", "err", err)
		}
	}
	s.render(w, r, http.StatusOK, "landing", data)
}
