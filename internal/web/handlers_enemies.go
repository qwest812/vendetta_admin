package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

// enemyCandidates ограничивает подбор в форме добавления. Список короче
// поискового: из него выбирают одного человека, а не изучают базу.
const enemyCandidates = 20

// enemiesList — личный список врагов. Каждый видит только свой, поэтому
// пользователь берётся из сессии, а не из адреса.
func (s *Server) enemiesList(w http.ResponseWriter, r *http.Request) {
	s.renderEnemies(w, r, http.StatusOK, "")
}

func (s *Server) renderEnemies(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	list, err := s.enemies.List(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "enemies", map[string]any{
		"Enemies": list, "Error": errMsg,
		// Пустой подбор: поле поиска ещё не трогали. Подбор живёт в HTMX и
		// формой не отправляется, а вот комментарий возвращаем в поле —
		// иначе после отказа его пришлось бы набирать заново.
		"Candidates": nil, "Marked": map[int64]bool{},
		"Query": "", "Limit": enemyCandidates,
		"Comment": r.PostFormValue("comment"),
	})
}

// enemiesSearch отвечает на живой ввод в форме добавления. Пустой запрос
// ничего не показывает: это подбор одного человека, а не витрина базы.
func (s *Server) enemiesSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var players []*domain.Player
	if query != "" {
		found, err := s.players.Search(r.Context(), query, "", enemyCandidates)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		players = found
	}

	marked, err := s.markedEnemies(r, players)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.renderPartial(w, r, "enemies", "candidates", map[string]any{
		"Candidates": players, "Marked": marked, "Query": query, "Limit": enemyCandidates,
	})
}

// markedEnemies отвечает, кто из выборки уже во врагах у того, кто смотрит:
// и подбор в форме добавления, и строка поиска не должны предлагать добавить
// того, кто уже добавлен.
func (s *Server) markedEnemies(r *http.Request, players []*domain.Player) (map[int64]bool, error) {
	ids := make([]int64, len(players))
	for i, p := range players {
		ids[i] = p.ID
	}
	return s.enemies.Marked(r.Context(), currentUser(r).ID, ids)
}

// enemyMark — кнопка «во враги» в строке поиска. Комментарий здесь не
// спрашивается: пометить надо в один клик, не бросая поиск, а «за что»
// дописывается в самом разделе.
func (s *Server) enemyMark(w http.ResponseWriter, r *http.Request) {
	playerID, ok := enemyPlayerID(w, r)
	if !ok {
		return
	}
	if _, err := s.players.ByID(r.Context(), playerID); errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Такой карточки в базе нет", http.StatusNotFound)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Повтор не ошибка: выдача поиска могла устареть, а нужное состояние
	// то же самое. Чужой комментарий при этом не трогается — Add его не пишет.
	err := s.enemies.Add(r.Context(), currentUser(r).ID, playerID, "")
	if err != nil && !errors.Is(err, domain.ErrEnemyExists) {
		s.serverError(w, r, err)
		return
	}

	// Без HTMX подменять в странице некому, и кусок разметки показывать
	// нечестно — отправляем в сам раздел, там видно, что получилось.
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/enemies", http.StatusSeeOther)
		return
	}
	s.renderPartial(w, r, "home", "mark", map[string]any{"ID": playerID, "Marked": true})
}

// enemyAdd записывает во враги карточку из базы. Игрока без карточки записать
// нельзя: список ссылается на карточки, а заводит их админ.
func (s *Server) enemyAdd(w http.ResponseWriter, r *http.Request) {
	playerID, err := strconv.ParseInt(r.PostFormValue("player_id"), 10, 64)
	if err != nil {
		s.renderEnemies(w, r, http.StatusUnprocessableEntity,
			"Выберите игрока из найденных — кнопкой «Добавить» в списке")
		return
	}
	comment, ok := s.enemyComment(w, r)
	if !ok {
		return
	}

	if _, err := s.players.ByID(r.Context(), playerID); errors.Is(err, domain.ErrNotFound) {
		s.renderEnemies(w, r, http.StatusNotFound, "Такой карточки в базе нет")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}

	err = s.enemies.Add(r.Context(), currentUser(r).ID, playerID, comment)
	if errors.Is(err, domain.ErrEnemyExists) {
		s.renderEnemies(w, r, http.StatusUnprocessableEntity,
			"Этот игрок уже у вас во врагах — комментарий правится прямо в списке")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/enemies", http.StatusSeeOther)
}

func (s *Server) enemyUpdate(w http.ResponseWriter, r *http.Request) {
	playerID, ok := enemyPlayerID(w, r)
	if !ok {
		return
	}
	comment, ok := s.enemyComment(w, r)
	if !ok {
		return
	}

	err := s.enemies.SetComment(r.Context(), currentUser(r).ID, playerID, comment)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Этого игрока нет в вашем списке врагов", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/enemies", http.StatusSeeOther)
}

func (s *Server) enemyRemove(w http.ResponseWriter, r *http.Request) {
	playerID, ok := enemyPlayerID(w, r)
	if !ok {
		return
	}
	err := s.enemies.Remove(r.Context(), currentUser(r).ID, playerID)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Этого игрока нет в вашем списке врагов", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/enemies", http.StatusSeeOther)
}

// enemyComment разбирает поле комментария. Пустой разрешён: иногда достаточно
// самой отметки.
func (s *Server) enemyComment(w http.ResponseWriter, r *http.Request) (string, bool) {
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	if len([]rune(comment)) > domain.MaxCommentLen {
		s.renderEnemies(w, r, http.StatusUnprocessableEntity,
			"Комментарий длиннее "+strconv.Itoa(domain.MaxCommentLen)+" символов")
		return "", false
	}
	return comment, true
}

func enemyPlayerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("playerID"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
