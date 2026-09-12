package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
)

const searchLimit = 50

// home — стартовый экран поиска.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	query, status := searchParams(r)
	players, err := s.findPlayers(r, query, status)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	total, err := s.players.Count(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	marks, err := s.marks(r, players)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", merge(marks, map[string]any{
		"Query": query, "Status": string(status), "Players": players,
		"Total": total, "Limit": searchLimit, "CanMark": true,
	}))
}

// search отвечает на живой ввод: HTMX подменяет только список результатов.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query, status := searchParams(r)
	players, err := s.findPlayers(r, query, status)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	marks, err := s.marks(r, players)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPartial(w, r, "home", "results", merge(marks, map[string]any{
		"Query": query, "Status": string(status),
		"Players": players, "Limit": searchLimit, "CanMark": true,
	}))
}

// marks — кто из выборки уже в личных списках смотрящего. Строка поиска
// показывает обе пометки сразу, поэтому и считаются они вместе.
func (s *Server) marks(r *http.Request, players []*domain.Player) (map[string]any, error) {
	enemies, err := s.enemies.marked(r, players)
	if err != nil {
		return nil, err
	}
	friends, err := s.friends.marked(r, players)
	if err != nil {
		return nil, err
	}
	// Запас карточек общий на оба списка, поэтому и гаснут пометки сразу
	// обе. Спрашиваем любой раздел — ответ у них один и тот же.
	used, limit, err := s.enemies.quota(r)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"MarkedEnemies": enemies, "MarkedFriends": friends,
		"EnemyWords": enemyWords, "FriendWords": friendWords,
		"RelationFull": limit > 0 && used >= limit,
	}, nil
}

// merge складывает две карты данных для шаблона: общее и то, что нужно
// конкретной странице.
func merge(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

// searchParams разбирает строку запроса. Незнакомый статус — это не ошибка,
// а «фильтр не выбран»: поиск не то место, где показывают 400.
func searchParams(r *http.Request) (string, domain.ClanStatus) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	status, ok := domain.ParseClanStatus(q.Get("status"))
	if !ok {
		status = ""
	}
	return query, status
}

// findPlayers ищет только когда есть о чём спрашивать: пустая строка и
// невыбранный статус — это ещё не запрос, и вываливать в ответ на них всю
// базу незачем.
func (s *Server) findPlayers(r *http.Request, query string,
	status domain.ClanStatus) ([]*domain.Player, error) {

	if query == "" && status == "" {
		return nil, nil
	}
	return s.players.Search(r.Context(), query, status, searchLimit)
}

func (s *Server) playerCard(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	s.renderPlayerCard(w, r, http.StatusOK, player, "", "", nil)
}

// renderPlayerCard собирает карточку. Отдельно от обработчика, потому что
// её же показывают формы признаков и комментария, когда с ними что-то
// не так: набранный текст и расставленные галочки при этом возвращаются
// на место — терять их из-за одной ошибки нельзя.
//
// marked пустой означает «взять как в базе»: так карточка открывается
// обычным заходом, а не после неудачной отправки формы.
func (s *Server) renderPlayerCard(w http.ResponseWriter, r *http.Request, status int,
	player *domain.Player, errMsg, body string, marked map[int64]bool) {

	me := currentUser(r)

	comments, err := s.players.Comments(r.Context(), player.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Признаки со счётчиками: сколько человек отметили каждый и кто именно.
	// Имена показываются только админам — в ленте и в счётчиках остальные
	// видят числа без подписей.
	marks, err := s.players.TraitMarks(r.Context(), player.ID, me.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	mine := make(map[int64]bool, len(marks))
	for _, m := range marks {
		if m.Mine {
			mine[m.ID] = true
		}
	}
	if marked == nil {
		marked = mine
	}

	// Свой комментарий нужен форме: она подставляет прежний текст и
	// предупреждает, что новый его заменит.
	var myComment *domain.Comment
	for i := range comments {
		if comments[i].Mine(me) {
			myComment = &comments[i]
			break
		}
	}
	if body == "" && myComment != nil {
		body = myComment.Body
	}

	// Что игра рассказала про этого человека, когда мы в последний раз
	// заходили в партию с ним. Ничего не рассказала — карточка просто
	// обходится без этого: связь идёт по игровому ID, а он есть не у всех.
	var seen *domain.GamePlayer
	// История банов: сегодняшний статус живёт в seen и снятие бана его
	// затирает, поэтому «сидел когда-то» видно только по журналу.
	var bans []domain.BanEvent
	if player.GameID != "" {
		known, err := s.gamePlayers.ByGameIDs(r.Context(), []string{player.GameID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if p, ok := known[player.GameID]; ok {
			seen = &p
		}
		bans, err = s.gamePlayers.BanHistory(r.Context(), player.GameID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	traits, err := s.traits.List(r.Context(), true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Герои: справочник плюс то, что о них отметили. Показываются все —
	// у неотмеченных выбран ноль, он же «героя у игрока нет».
	heroRows, err := s.heroRows(r, player.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Скольких героев вообще отмечали: блок свёрнут, и это число —
	// единственное, что видно о нём в закрытом виде.
	heroesMarked := 0
	for _, h := range heroRows {
		if h.Count > 0 {
			heroesMarked++
		}
	}

	// Боевой счёт с сайта игры: уровень и кд. Считает его страница партии,
	// когда видит игрока на карте, — здесь только показываем уже
	// посчитанное, в сеть за ним не ходим.
	var stats domain.UserStats
	if player.GameID != "" && s.userStats != nil {
		known, err := s.userStats.Known(r.Context(), []string{player.GameID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		stats = known[player.GameID]
	}

	s.render(w, r, status, "player", map[string]any{
		"Player": player, "Comments": comments, "MyComment": myComment,
		"Error": errMsg, "Seen": seen, "Bans": bans, "Traits": traits,
		"Marks": marks, "Stats": stats, "Marked": marked, "Body": body,
		"Heroes": heroRows, "HeroesMarked": heroesMarked,
		"CommentMax": domain.CommentMaxLen,
	})
}

func (s *Server) playerNew(w http.ResponseWriter, r *http.Request) {
	s.renderPlayerForm(w, r, http.StatusOK, nil)
}

func (s *Server) playerEdit(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	s.renderPlayerForm(w, r, http.StatusOK, player)
}

// renderPlayerForm обслуживает и создание, и правку: отличаются только
// заголовком и адресом отправки.
func (s *Server) renderPlayerForm(w http.ResponseWriter, r *http.Request, status int, player *domain.Player, errs ...string) {
	clans, err := s.clans.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := ""
	if len(errs) > 0 {
		msg = errs[0]
	}
	s.render(w, r, status, "player_form", map[string]any{
		"Player": player, "Clans": clans, "Error": msg,
	})
}

func (s *Server) playerCreate(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))
	nickname := strings.TrimSpace(r.PostFormValue("nickname"))
	clan := strings.TrimSpace(r.PostFormValue("clan"))
	fail := func(msg string) {
		s.renderPlayerForm(w, r, http.StatusUnprocessableEntity,
			&domain.Player{GameID: gameID, Nickname: nickname, ClanName: clan}, msg)
	}
	// У новой карточки игровой ID обязателен: ник игрок может сменить,
	// и без ID карточку потом не опознать.
	if err := validateGameID(gameID, true); err != nil {
		fail(errText(r, err))
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(errText(r, err))
		return
	}

	player, err := s.players.Create(r.Context(), gameID, nickname, clan, actor.ID)
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail(langOf(r).T("err.player.id.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Первый комментарий не обязателен, но обычно он и есть повод завести
	// карточку. Слишком длинный обрезаем, а не отвергаем: карточка уже
	// заведена, и отказывать из-за лишних символов поздно.
	if body := strings.TrimSpace(r.PostFormValue("note")); body != "" {
		if _, err := s.players.SaveComment(r.Context(), player.ID, actor.ID, cut(body, domain.CommentMaxLen)); err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	s.logAuditOn(r, "player.create", "player", player.ID,
		map[string]any{"game_id": gameID, "nickname": nickname, "clan": clan})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

func (s *Server) playerUpdate(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))
	nickname := strings.TrimSpace(r.PostFormValue("nickname"))
	clan := strings.TrimSpace(r.PostFormValue("clan"))
	fail := func(msg string) {
		s.renderPlayerForm(w, r, http.StatusUnprocessableEntity,
			&domain.Player{ID: player.ID, GameID: gameID, Nickname: nickname, ClanName: clan}, msg)
	}
	// ID обязателен и при правке: иначе карточки, заведённые до его
	// появления, так и остались бы без него. Правка — тот самый момент,
	// когда его удобно дописать.
	if err := validateGameID(gameID, true); err != nil {
		fail(errText(r, err))
		return
	}
	if err := validateNickname(nickname); err != nil {
		fail(errText(r, err))
		return
	}

	err := s.players.Update(r.Context(), player.ID, gameID, nickname, clan)
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail(langOf(r).T("err.player.id.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditOn(r, "player.update", "player", player.ID, map[string]any{
		"game_id": gameID, "nickname": nickname, "was": player.Nickname,
		"clan": clan,
	})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

func (s *Server) playerDelete(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	if err := s.players.Delete(r.Context(), player.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "player.delete", "player", player.ID, map[string]any{"nickname": player.Nickname})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// playerHeroView — строка героя в карточке: кто он, до какого уровня
// качается и что о нём отметили. Справочник героев живёт отдельно от базы
// отметок, и сводит их карточка.
type playerHeroView struct {
	UnitTypeID int
	Name       string
	Subtitle   string
	Image      string
	// Max — сколько у героя ступеней: у Каллахана их десять, у Мейв
	// пятнадцать, у большинства двадцать. Ноль означает, что справочник
	// про уровни этого героя ещё ничего не знает.
	Max int
	// Levels — номера ступеней от нуля: по ним рисуется ряд переключателей.
	Levels []int
	// Level — сводный уровень (что назвали чаще), Count — сколько человек
	// отметили героя, Mine — что поставил я.
	Level int
	Count int
	Mine  int
}

// heroRows сводит справочник героев с отметками этого игрока. Порядок —
// справочника: он идёт по номеру типа юнита, то есть по тому, в каком
// порядке героев добавляли в игру.
func (s *Server) heroRows(r *http.Request, playerID int64) ([]playerHeroView, error) {
	snap, _, err := s.heroSnapshot(r.Context())
	if err != nil {
		return nil, err
	}
	marks, err := s.players.HeroMarks(r.Context(), playerID, currentUser(r).ID)
	if err != nil {
		return nil, err
	}
	byType := make(map[int]domain.HeroMark, len(marks))
	for _, m := range marks {
		byType[m.UnitTypeID] = m
	}

	ru := langOf(r) == i18n.RU
	out := make([]playerHeroView, 0, len(snap.Heroes))
	for _, h := range snap.Heroes {
		row := playerHeroView{
			UnitTypeID: h.UnitTypeID, Image: h.Image, Max: len(h.Levels),
			Name:     pick(ru, h.ShortRu, h.ShortEn),
			Subtitle: pick(ru, h.SubtitleRu, h.SubtitleEn),
		}
		// Нулевая ступень — «героя нет»: в справочнике её не бывает,
		// а в форме она первая и выбрана по умолчанию.
		for i := 0; i <= row.Max; i++ {
			row.Levels = append(row.Levels, i)
		}
		if m, ok := byType[h.UnitTypeID]; ok {
			row.Level, row.Count, row.Mine = m.Level, m.Count, m.Mine
		}
		out = append(out, row)
	}
	return out, nil
}

// pick выбирает перевод: русский, если он есть, иначе английский. Игра
// переводит своих героев сама, и часть имён у неё осталась непереведённой.
func pick(ru bool, russian, english string) string {
	if ru && russian != "" {
		return russian
	}
	if english != "" {
		return english
	}
	return russian
}

// heroesSave сохраняет мои уровни героев. Форма приходит полным состоянием:
// у каждого героя выбрана ровно одна ступень, ноль означает «героя у него
// нет». Правим только то, что действительно изменилось, — иначе журнал
// распухал бы на два десятка строк с каждой отправки.
func (s *Server) heroesSave(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	me := currentUser(r)

	rows, err := s.heroRows(r, player.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	for _, row := range rows {
		want, err := strconv.Atoi(r.PostFormValue(heroField(row.UnitTypeID)))
		if err != nil {
			// Героя в форме не было или пришло не число — не трогаем:
			// молчаливое обнуление стёрло бы чужую работу.
			continue
		}
		if want < 0 || (row.Max > 0 && want > row.Max) {
			continue
		}
		if want == row.Mine {
			continue
		}

		changed, err := s.players.SetHeroLevel(r.Context(), player.ID, row.UnitTypeID, me.ID, want)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !changed {
			continue
		}
		action := "player.hero.set"
		if want == 0 {
			action = "player.hero.clear"
		}
		s.logAuditOn(r, action, "player", player.ID,
			map[string]any{"nickname": player.Nickname, "hero": row.Name, "level": want})
	}

	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

// heroField — имя поля переключателей одного героя. Одно место на шаблон
// и обработчик: разъехавшись, они молча перестали бы видеть выбор.
func heroField(unitTypeID int) string { return "hero-" + strconv.Itoa(unitTypeID) }

// traitsSave сохраняет мои отметки признаков. Отметка личная: один и тот же
// признак ставят разные люди, и в карточке напротив него стоит их число.
// Чужие отметки эта форма не трогает — снимает их админ, каждую своей
// кнопкой.
//
// Отмечать может любой, у кого есть доступ: замечает поведение тот, кто
// играет рядом. Поэтому каждая поставленная и снятая отметка идёт в журнал.
func (s *Server) traitsSave(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	me := currentUser(r)

	// Галочки приходят полным состоянием формы: отмеченные есть в запросе,
	// снятые — нет. Сверяем их со своими и правим разницу.
	want := map[int64]bool{}
	for _, v := range r.PostForm["traits"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			want[id] = true
		}
	}

	marks, err := s.players.TraitMarks(r.Context(), player.ID, me.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	has := map[int64]bool{}
	for _, m := range marks {
		if m.Mine {
			has[m.ID] = true
		}
	}

	all, err := s.traits.List(r.Context(), true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	for _, tr := range all {
		on := want[tr.ID]
		if on == has[tr.ID] {
			continue
		}
		var changed bool
		if on {
			changed, err = s.players.MarkTrait(r.Context(), player.ID, tr.ID, me.ID)
		} else {
			changed, err = s.players.UnmarkTrait(r.Context(), player.ID, tr.ID, me.ID)
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if !changed {
			continue
		}
		action := "player.trait.remove"
		if on {
			action = "player.trait.add"
		}
		s.logAuditOn(r, action, "player", player.ID,
			map[string]any{"nickname": player.Nickname, "trait": tr.Name})
	}

	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

// traitDropAll снимает признак у всех, кто его отметил, — это админское
// средство против наговора. Разбирать по одному не даём: карточка не место
// для спора о том, кто из пятерых погорячился.
func (s *Server) traitDropAll(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	traitID, err := strconv.ParseInt(r.PathValue("traitID"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return
	}
	trait, err := s.traits.ByID(r.Context(), traitID)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Признак не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	dropped, err := s.players.UnmarkTraitAll(r.Context(), player.ID, traitID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if dropped > 0 {
		s.logAuditOn(r, "player.trait.dropall", "player", player.ID,
			map[string]any{"nickname": player.Nickname, "trait": trait.Name, "marks": dropped})
	}
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

// commentSave пишет комментарий об игроке. У каждого он один: новый текст
// заменяет прежний и всплывает наверх ленты — форма предупреждает об этом
// заранее, поэтому здесь замена уже не спрашивается.
func (s *Server) commentSave(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))

	fail := func(msg string) {
		s.renderPlayerCard(w, r, http.StatusUnprocessableEntity, player, msg, body, nil)
	}
	if body == "" {
		fail(langOf(r).T("err.comment.empty"))
		return
	}
	if len([]rune(body)) > domain.CommentMaxLen {
		fail(langOf(r).T("err.comment.length", domain.CommentMaxLen))
		return
	}

	id, err := s.players.SaveComment(r.Context(), player.ID, currentUser(r).ID, body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "comment.save", "comment", id, map[string]any{"player": player.Nickname})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(player.ID, 10), http.StatusSeeOther)
}

// commentDelete: свой комментарий убирает автор, чужой — админ и выше.
// Кто автор, из адреса не видно: удаляется он по своему номеру, а не
// по имени написавшего.
func (s *Server) commentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("commentID"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return
	}
	comment, err := s.players.CommentByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Комментарий не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	if !comment.CanDelete(currentUser(r)) {
		http.Error(w, "Чужой комментарий может удалить только админ", http.StatusForbidden)
		return
	}
	if err := s.players.DeleteComment(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	// В журнале — чей комментарий убрали: своё удаление от чужого иначе
	// не отличить, а чужое и есть то, за чем в журнал заглядывают.
	s.logAuditOn(r, "comment.delete", "comment", id, map[string]any{"author": comment.AuthorName})
	http.Redirect(w, r, "/players/"+strconv.FormatInt(comment.PlayerID, 10), http.StatusSeeOther)
}

// cut обрезает текст до допустимой длины по символам, а не по байтам:
// кириллица иначе рвалась бы посередине буквы.
func cut(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func (s *Server) loadPlayer(w http.ResponseWriter, r *http.Request) (*domain.Player, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return nil, false
	}
	player, err := s.players.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Игрок не найден", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return player, true
}

func validateNickname(nick string) error {
	switch n := len([]rune(nick)); {
	case n == 0:
		return domain.ErrPlayerNickRequired
	case n > 64:
		return domain.ErrPlayerNickTooLong
	}
	return nil
}

// validateGameID проверяет игровой ID: он попадает в поиск наравне с ником,
// поэтому пробелы внутри не допускаются — иначе по нему не найти.
func validateGameID(gameID string, required bool) error {
	if gameID == "" {
		if required {
			return domain.ErrPlayerIDRequired
		}
		return nil
	}
	if len([]rune(gameID)) > 32 {
		return domain.ErrPlayerIDTooLong
	}
	if strings.ContainsAny(gameID, " \t\n") {
		return domain.ErrPlayerIDSpaces
	}
	return nil
}
