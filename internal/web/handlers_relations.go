package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// relationCandidates ограничивает подбор в форме добавления. Список короче
// поискового: из него выбирают одного человека, а не изучают базу.
const relationCandidates = 20

// relationWords — слова раздела. Списки врагов и друзей устроены одинаково,
// а говорят о разном, и подставить «врагов» в страницу друзей нельзя, поэтому
// всё, что отличается, собрано здесь.
//
// Здесь не сам текст, а ключи сообщений: страницу читают на разных языках,
// и раздел не должен решать, на каком именно.
type relationWords struct {
	Path      string // корень путей и имя шаблона: "enemies" или "friends"
	Title     string // заголовок страницы
	Intro     string // абзац под заголовком
	Pick      string // подпись выпадающего списка
	Empty     string // подпись пустого списка
	AllListed string // все найденные уже добавлены
	Already   string // повторное добавление
	Missing   string // игрока нет в списке
	Confirm   string // вопрос перед удалением, %s — ник
	// Пометка в строке поиска: подпись кнопки, подпись уже добавленного
	// и подсказки к ним.
	MarkAdd   string
	MarkDone  string
	AddTitle  string
	DoneTitle string
	// Class — цвет пометки и обводки: "enemy" или "friend".
	Class string
}

var enemyWords = relationWords{
	Path:      "enemies",
	Title:     "rel.enemies.title",
	Intro:     "rel.enemies.intro",
	Pick:      "rel.enemies.pick",
	Empty:     "rel.enemies.empty",
	AllListed: "rel.enemies.alllisted",
	Already:   "rel.enemies.already",
	Missing:   "rel.enemies.missing",
	Confirm:   "rel.enemies.confirm",
	MarkAdd:   "rel.enemies.markadd",
	MarkDone:  "rel.enemies.markdone",
	AddTitle:  "rel.enemies.addtitle",
	DoneTitle: "rel.enemies.donetitle",
	Class:     "enemy",
}

var friendWords = relationWords{
	Path:      "friends",
	Title:     "rel.friends.title",
	Intro:     "rel.friends.intro",
	Pick:      "rel.friends.pick",
	Empty:     "rel.friends.empty",
	AllListed: "rel.friends.alllisted",
	Already:   "rel.friends.already",
	Missing:   "rel.friends.missing",
	Confirm:   "rel.friends.confirm",
	MarkAdd:   "rel.friends.markadd",
	MarkDone:  "rel.friends.markdone",
	AddTitle:  "rel.friends.addtitle",
	DoneTitle: "rel.friends.donetitle",
	Class:     "friend",
}

// relationSection — раздел личного списка: «Враги» или «Друзья». Устроены они
// одинаково — поиск, выпадающий список, комментарий, — поэтому обработчики
// общие, а различаются таблицей в базе и словами на странице.
type relationSection struct {
	srv   *Server
	repo  *repo.Relations
	words relationWords
}

func newRelationSection(srv *Server, r *repo.Relations, words relationWords) *relationSection {
	return &relationSection{srv: srv, repo: r, words: words}
}

// list — личный список. Каждый видит только свой, поэтому пользователь
// берётся из сессии, а не из адреса.
func (rs *relationSection) list(w http.ResponseWriter, r *http.Request) {
	rs.render(w, r, http.StatusOK, "")
}

func (rs *relationSection) render(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	list, err := rs.repo.List(r.Context(), currentUser(r).ID)
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}

	// Страница открывается с пустым подбором: пока не набран запрос,
	// предлагать некого. Иначе свежая карточка стояла бы первой строкой
	// и её принимали за уже добавленного.
	data, err := rs.pick(r, "")
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	data["List"] = list
	data["Error"] = errMsg
	// Подбор живёт в HTMX и формой не отправляется, а вот комментарий
	// возвращаем в поле — иначе после отказа его пришлось бы набирать заново.
	data["Comment"] = r.PostFormValue("comment")

	rs.srv.render(w, r, status, rs.words.Path, data)
}

// quota — сколько карточек человек занял и сколько ему можно. Предел нулевой
// означает «без предела»: так отвечает и ультра-пакет, и рут, которого
// не считают вовсе.
func (rs *relationSection) quota(r *http.Request) (used, limit int, err error) {
	me := currentUser(r)
	limit = me.RelationLimit()
	if limit == 0 {
		// Считать занятое незачем: показывать его не с чем, а запрос
		// к базе на каждой странице стоит денег.
		return 0, 0, nil
	}
	used, err = rs.repo.UsedTotal(r.Context(), me.ID)
	return used, limit, err
}

// pick — данные подбора: кого нашли по запросу, кто из них уже в списке
// и есть ли кого добавлять. Пустой запрос ничего не ищет: подбор — ответ
// на набранное, а не витрина базы.
func (rs *relationSection) pick(r *http.Request, query string) (map[string]any, error) {
	// Запас общий на оба списка, поэтому считается он не по .List: в «Друзьях»
	// надо видеть и то, что съели враги, иначе отказ выглядит беспричинным.
	// Считается он здесь, а не в render: подбор перерисовывается на живой
	// ввод, и кнопка «Добавить» должна гаснуть там же, где гаснут пометки.
	used, limit, err := rs.quota(r)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Words": rs.words, "Candidates": nil, "Marked": map[int64]bool{},
		"Query": query, "Limit": relationCandidates, "CanAdd": false,
		"PlanUsed": used, "PlanLimit": limit, "PlanFull": limit > 0 && used >= limit,
	}
	if query == "" {
		return data, nil
	}

	players, err := rs.srv.players.Search(r.Context(), query, "", relationCandidates)
	if err != nil {
		return nil, err
	}
	marked, err := rs.marked(r, players)
	if err != nil {
		return nil, err
	}
	// Кнопку показываем, только если есть кого выбрать: когда все найденные
	// уже в списке, добавлять нечего.
	for _, p := range players {
		if !marked[p.ID] {
			data["CanAdd"] = true
			break
		}
	}
	data["Candidates"], data["Marked"] = players, marked
	return data, nil
}

// search отвечает на живой ввод в форме добавления: подходящие карточки
// складываются в выпадающий список рядом с кнопкой «Добавить». Стёртая
// строка возвращает подбор в исходный вид — пустым.
func (rs *relationSection) search(w http.ResponseWriter, r *http.Request) {
	data, err := rs.pick(r, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	rs.srv.renderPartial(w, r, rs.words.Path, "candidates", data)
}

// marked отвечает, кто из выборки уже в списке у того, кто смотрит: и подбор
// в форме добавления, и строка поиска не должны предлагать добавить того,
// кто уже добавлен.
func (rs *relationSection) marked(r *http.Request, players []*domain.Player) (map[int64]bool, error) {
	ids := make([]int64, len(players))
	for i, p := range players {
		ids[i] = p.ID
	}
	return rs.repo.Marked(r.Context(), currentUser(r).ID, ids)
}

// mark — кнопка в строке поиска. Комментарий здесь не спрашивается: пометить
// надо в один клик, не бросая поиск, а «за что» дописывается в самом разделе.
func (rs *relationSection) mark(w http.ResponseWriter, r *http.Request) {
	playerID, ok := relationPlayerID(w, r)
	if !ok {
		return
	}
	if _, err := rs.srv.players.ByID(r.Context(), playerID); errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Такой карточки в базе нет", http.StatusNotFound)
		return
	} else if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}

	// Повтор не ошибка: выдача поиска могла устареть, а нужное состояние
	// то же самое. Чужой комментарий при этом не трогается — Add его не пишет.
	err := rs.repo.Add(r.Context(), currentUser(r).ID, playerID, "", currentUser(r).RelationLimit())
	full := errors.Is(err, domain.ErrPlanLimit)
	if err != nil && !full && !errors.Is(err, domain.ErrAlreadyListed) {
		rs.srv.serverError(w, r, err)
		return
	}

	// Без HTMX подменять в странице некому, и кусок разметки показывать
	// нечестно — отправляем в сам раздел, там видно, что получилось.
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/"+rs.words.Path, http.StatusSeeOther)
		return
	}
	// Отказ по пределу возвращает ту же кнопку, но погашенной и с причиной
	// в подсказке. Код при этом честный: нажатие не сработало.
	status := http.StatusOK
	if full {
		status = http.StatusUnprocessableEntity
	}
	rs.srv.renderPartialStatus(w, r, status, "home", "mark",
		rs.markData(playerID, !full, full, csrfToken(r)))
}

// markData — данные пометки одной строки поиска. Собираются в одном месте:
// их отдают и список результатов, и ответ на нажатие.
//
// marked и full — разные состояния: первое значит «уже в списке», второе —
// «добавить некуда». Вместе они не встречаются.
func (rs *relationSection) markData(playerID int64, marked, full bool, csrf string) map[string]any {
	return map[string]any{
		"ID": playerID, "Marked": marked, "Full": full,
		"CSRFToken": csrf, "Words": rs.words,
	}
}

// add записывает в список карточку из базы. Игрока без карточки записать
// нельзя: список ссылается на карточки, а заводит их админ.
func (rs *relationSection) add(w http.ResponseWriter, r *http.Request) {
	playerID, err := strconv.ParseInt(r.PostFormValue("player_id"), 10, 64)
	if err != nil {
		rs.render(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.rel.pick"))
		return
	}
	comment, ok := rs.comment(w, r)
	if !ok {
		return
	}

	if _, err := rs.srv.players.ByID(r.Context(), playerID); errors.Is(err, domain.ErrNotFound) {
		rs.render(w, r, http.StatusNotFound, langOf(r).T("err.rel.nocard"))
		return
	} else if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}

	me := currentUser(r)
	err = rs.repo.Add(r.Context(), me.ID, playerID, comment, me.RelationLimit())
	if errors.Is(err, domain.ErrAlreadyListed) {
		rs.render(w, r, http.StatusUnprocessableEntity, langOf(r).T(rs.words.Already))
		return
	}
	if errors.Is(err, domain.ErrPlanLimit) {
		rs.render(w, r, http.StatusUnprocessableEntity,
			langOf(r).T("err.plan.limit", me.RelationLimit()))
		return
	}
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/"+rs.words.Path, http.StatusSeeOther)
}

func (rs *relationSection) update(w http.ResponseWriter, r *http.Request) {
	playerID, ok := relationPlayerID(w, r)
	if !ok {
		return
	}
	comment, ok := rs.comment(w, r)
	if !ok {
		return
	}

	err := rs.repo.SetComment(r.Context(), currentUser(r).ID, playerID, comment)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, langOf(r).T(rs.words.Missing), http.StatusNotFound)
		return
	}
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/"+rs.words.Path, http.StatusSeeOther)
}

func (rs *relationSection) remove(w http.ResponseWriter, r *http.Request) {
	playerID, ok := relationPlayerID(w, r)
	if !ok {
		return
	}
	err := rs.repo.Remove(r.Context(), currentUser(r).ID, playerID)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, langOf(r).T(rs.words.Missing), http.StatusNotFound)
		return
	}
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/"+rs.words.Path, http.StatusSeeOther)
}

// comment разбирает поле комментария. Пустой разрешён: иногда достаточно
// самой отметки.
func (rs *relationSection) comment(w http.ResponseWriter, r *http.Request) (string, bool) {
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	if len([]rune(comment)) > domain.MaxCommentLen {
		rs.render(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.rel.comment", domain.MaxCommentLen))
		return "", false
	}
	return comment, true
}

func relationPlayerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("playerID"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
