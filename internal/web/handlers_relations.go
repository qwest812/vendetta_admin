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
	// NoteAdd — кнопка той же заметки на карточке игрока. Там она стоит
	// не в тесной строке поиска, а в форме, и слова ей нужны полные.
	NoteAdd string
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
	NoteAdd:   "rel.enemies.noteadd",
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
	NoteAdd:   "rel.friends.noteadd",
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

	players, err := rs.srv.players.Search(r.Context(), query, relationCandidates)
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

// mark — сохранение заметки из строки поиска. Кнопка в строке ничего
// не записывает: она открывает форму (markForm), и только «Сохранить»
// доходит сюда. Поэтому один и тот же обработчик и добавляет впервые,
// и правит заметку у того, кто в списке уже есть.
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

	me := currentUser(r)
	hx := r.Header.Get("HX-Request") != ""
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	if len([]rune(comment)) > domain.MaxCommentLen {
		msg := langOf(r).T("err.rel.comment", domain.MaxCommentLen)
		// Набранное возвращается на место: терять длинную заметку из-за
		// того, что она длинная, — худшее, что тут можно сделать.
		if !hx {
			rs.render(w, r, http.StatusUnprocessableEntity, msg)
			return
		}
		data := rs.markData(playerID, false, false, csrfToken(r))
		data["Form"], data["Comment"], data["Error"] = true, comment, msg
		rs.srv.renderPartialStatus(w, r, http.StatusUnprocessableEntity, "home", "mark", data)
		return
	}

	full := false
	err := rs.repo.Add(r.Context(), me.ID, playerID, comment, me.RelationLimit())
	switch {
	case errors.Is(err, domain.ErrAlreadyListed):
		// Повтор — это правка: форму открыли с прежним текстом, значит
		// новый его и заменяет. Запись успели убрать в другой вкладке —
		// тоже не беда: править нечего, и это не ошибка.
		if err := rs.repo.SetComment(r.Context(), me.ID, playerID, comment); err != nil &&
			!errors.Is(err, domain.ErrNotFound) {
			rs.srv.serverError(w, r, err)
			return
		}
	case errors.Is(err, domain.ErrPlanLimit):
		full = true
	case err != nil:
		rs.srv.serverError(w, r, err)
		return
	}

	// Без HTMX подменять в странице некому: форму показывала карточка
	// игрока, туда и возвращаемся. Поля back нет — значит форму отправили
	// из раздела, и место ответа там.
	if !hx {
		http.Redirect(w, r, formBack(r, "/"+rs.words.Path), http.StatusSeeOther)
		return
	}
	// Отказ по пределу возвращает кнопку погашенной и с причиной в подсказке.
	// Код при этом честный: нажатие не сработало.
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
		"CommentMax": domain.MaxCommentLen,
	}
}

// markForm раскрывает в строке поиска форму заметки вместо кнопки. Открывают
// её и на «во враги», и на «во врагах»: в первом случае поле пустое, во
// втором — с тем, что уже написано, и «Сохранить» текст заменит.
func (rs *relationSection) markForm(w http.ResponseWriter, r *http.Request) {
	playerID, ok := relationPlayerID(w, r)
	if !ok {
		return
	}
	comment, listed, full, err := rs.markState(r, playerID)
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	// Места нет — форму открывать незачем: сохранить её всё равно не выйдет.
	if full {
		rs.srv.renderPartial(w, r, "home", "mark", rs.markData(playerID, false, true, csrfToken(r)))
		return
	}
	data := rs.markData(playerID, listed, false, csrfToken(r))
	data["Form"], data["Comment"] = true, comment
	rs.srv.renderPartial(w, r, "home", "mark", data)
}

// markBack сворачивает форму обратно в кнопку — это «Отмена». Состояние
// перечитывается, а не помнится страницей: пока форма была открыта, игрока
// могли добавить или убрать в другой вкладке.
func (rs *relationSection) markBack(w http.ResponseWriter, r *http.Request) {
	playerID, ok := relationPlayerID(w, r)
	if !ok {
		return
	}
	_, listed, full, err := rs.markState(r, playerID)
	if err != nil {
		rs.srv.serverError(w, r, err)
		return
	}
	rs.srv.renderPartial(w, r, "home", "mark", rs.markData(playerID, listed, full, csrfToken(r)))
}

// markState — всё, что нужно знать про пометку одного игрока: своя заметка,
// есть ли запись и не кончился ли запас. Запас спрашивается только у того,
// кого ещё не добавили: добавленному он уже не помеха.
func (rs *relationSection) markState(r *http.Request, playerID int64) (comment string, listed, full bool, err error) {
	comment, listed, err = rs.repo.Comment(r.Context(), currentUser(r).ID, playerID)
	if err != nil || listed {
		return comment, listed, false, err
	}
	used, limit, err := rs.quota(r)
	if err != nil {
		return "", false, false, err
	}
	return "", false, limit > 0 && used >= limit, nil
}

// note — своя заметка об игроке для его карточки. Там обе они стоят рядом,
// врагов и друзей, поэтому раздел отдаёт свою вместе со своими словами.
func (rs *relationSection) note(r *http.Request, playerID int64) (relationNote, error) {
	comment, listed, err := rs.repo.Comment(r.Context(), currentUser(r).ID, playerID)
	return relationNote{Words: rs.words, Listed: listed, Comment: comment}, err
}

// relationNotes — обе личные заметки об игроке для его карточки: и во
// врагах, и в друзьях. Порядок тот же, что у пометок в строке поиска.
func (s *Server) relationNotes(r *http.Request, playerID int64) ([]relationNote, error) {
	out := make([]relationNote, 0, 2)
	for _, sec := range []*relationSection{s.enemies, s.friends} {
		note, err := sec.note(r, playerID)
		if err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, nil
}

// relationNote — личная заметка об игроке в одном из списков: есть ли запись
// и что в ней написано. Видит её только хозяин списка — ни другой человек,
// ни админ, ни рут за чужими списками в базу не ходят.
type relationNote struct {
	Words   relationWords
	Listed  bool
	Comment string
}

// formBack — куда вернуться после отправки формы без HTMX. Ту же заметку правят
// и в разделе, и на карточке игрока, и возвращать всегда в раздел значило бы
// уводить человека со страницы, на которой он работал. Чужие адреса сюда
// не пускаем: принимается только путь карточки.
func formBack(r *http.Request, def string) string {
	back := r.PostFormValue("back")
	if id, ok := strings.CutPrefix(back, "/players/"); ok && digitsOnly(id) {
		return back
	}
	return def
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
