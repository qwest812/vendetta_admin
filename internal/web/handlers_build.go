package web

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy"
)

// Очередь строительства на странице партии. Ведёт её рут, а ставит здания
// воркер (supremacy.Builder): заход в партию игра засчитывает как вход,
// и страница ради одной кнопки туда не ходит.
//
// Поэтому список провинций и справочник зданий берутся из состояния партии,
// уже открытого на этой странице: без карты добавлять некуда, и форма
// об этом говорит прямо. Сама очередь лежит в базе и показывается всегда.

// buildNameMax — предел названия, которое приходит вместе с номером
// из формы. Названия провинций и зданий короткие; длиннее — мусор.
const buildNameMax = 100

// buildGroup — очередь одной провинции для страницы.
type buildGroup struct {
	ProvinceID int
	Province   string
	Entries    []domain.BuildEntry
}

// buildProvince — своя провинция в форме добавления: что в ней строится
// сейчас и свободен ли слот. Берётся из состояния партии, поэтому
// появляется только вместе с картой.
type buildProvince struct {
	ID    int
	Name  string
	Now   string    // что строится сейчас; пусто — ничего
	Ends  time.Time // когда кончится
	Queue int       // сколько уже в очереди
}

// buildGroups раскладывает очередь партии по провинциям, сохраняя порядок
// записей. Провинции идут так же, как их отдал репозиторий — по названию.
func buildGroups(entries []domain.BuildEntry) []buildGroup {
	var out []buildGroup
	for _, e := range entries {
		if n := len(out); n > 0 && out[n-1].ProvinceID == e.ProvinceID {
			out[n-1].Entries = append(out[n-1].Entries, e)
			continue
		}
		name := e.Province
		if name == "" {
			name = "провинция " + strconv.Itoa(e.ProvinceID)
		}
		out = append(out, buildGroup{ProvinceID: e.ProvinceID, Province: name,
			Entries: []domain.BuildEntry{e}})
	}
	return out
}

// buildProvinces — наши провинции из состояния партии, по названию.
// Пусто, если партия не наша или состояние не читалось.
func buildProvinces(state *supremacy.GameState, entries []domain.BuildEntry) []buildProvince {
	if state == nil || state.Me <= 0 {
		return nil
	}
	queued := map[int]int{}
	for _, e := range entries {
		queued[e.ProvinceID]++
	}

	var out []buildProvince
	for _, p := range state.Provinces {
		if p.Owner != state.Me {
			continue
		}
		view := buildProvince{ID: p.ID, Name: p.Name, Queue: queued[p.ID]}
		if len(p.Building) > 0 {
			// Показываем самую раннюю: по ней и считается, когда
			// освободится слот.
			ends := p.BuildsUntil()
			for _, c := range p.Building {
				if c.Ends.Equal(ends) {
					view.Now, view.Ends = state.Upgrades[c.UpgradeID].Name, ends
				}
			}
			if view.Now == "" {
				view.Now = "стройка"
				view.Ends = ends
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildUpgrade — здание в списке выбора: номер и подпись. Уровни игра
// держит отдельными зданиями с одним названием, поэтому со второго уровня
// он приписывается к подписи — иначе в списке стояло бы пять одинаковых
// «Крепостей», и какая из них какая, было бы не понять.
type buildUpgrade struct {
	ID    int
	Label string
}

// buildUpgrades — справочник зданий партии по подписи. Он приходит вместе
// с состоянием, поэтому названия в нём на языке аккаунта, а цены — те, что
// действуют в этой партии.
func buildUpgrades(state *supremacy.GameState, lang i18n.Lang) []buildUpgrade {
	if state == nil {
		return nil
	}
	out := make([]buildUpgrade, 0, len(state.Upgrades))
	for _, u := range state.Upgrades {
		if u.Name == "" {
			continue
		}
		label := u.Name
		if u.Tier > 1 {
			label = lang.T("game.build.tier", u.Name, u.Tier)
		}
		out = append(out, buildUpgrade{ID: u.ID, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// buildData — то, что страница партии знает об очереди. Состояние
// необязательно: без него показывается только сама очередь.
func (s *Server) buildData(r *http.Request, gameID string, state *supremacy.GameState) (map[string]any, error) {
	if s.builds == nil {
		return map[string]any{"BuildQueue": nil, "BuildProvinces": nil, "BuildUpgrades": nil}, nil
	}
	entries, err := s.builds.List(r.Context(), gameID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"BuildQueue":     buildGroups(entries),
		"BuildProvinces": buildProvinces(state, entries),
		"BuildUpgrades":  buildUpgrades(state, langOf(r)),
	}, nil
}

// gameBuildAdd дописывает здание в конец очереди провинции.
//
// Номер и название приходят одним полем («16|Крепость»): выбор в форме
// собран из состояния партии, а страница обновляет после этого только
// список очереди — второй раз заходить в партию ради названий нельзя,
// заход стоит входа в игру.
func (s *Server) gameBuildAdd(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	provinceID, province, okProvince := idAndName(r.PostFormValue("province"))
	upgradeID, upgrade, okUpgrade := idAndName(r.PostFormValue("upgrade"))
	if !okProvince || !okUpgrade {
		s.buildFailed(w, r, gameID, langOf(r).T("game.build.bad"))
		return
	}

	err := s.builds.Add(r.Context(), domain.BuildEntry{
		GameID: gameID, ProvinceID: provinceID, Province: province,
		UpgradeID: upgradeID, Upgrade: upgrade,
	}, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.buildBack(w, r, gameID, "")
}

func (s *Server) gameBuildRemove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		s.buildFailed(w, r, gameID, langOf(r).T("game.build.bad"))
		return
	}
	if err := s.builds.Remove(r.Context(), gameID, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.buildBack(w, r, gameID, "")
}

// gameBuildMove переставляет запись на шаг вверх или вниз. Крайнюю двигать
// некуда, и это не ошибка: стрелка просто ничего не делает.
func (s *Server) gameBuildMove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		s.buildFailed(w, r, gameID, langOf(r).T("game.build.bad"))
		return
	}
	up := r.PostFormValue("dir") != "down"
	if err := s.builds.Move(r.Context(), gameID, id, up); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.buildBack(w, r, gameID, "")
}

// gameBuildNow зовёт воркер в партию, не дожидаясь срока. Сам заход
// страница не делает: воркер мог бы в ту же минуту пойти туда же, и два
// захода подряд игра засчитала бы как два входа. Поэтому срок просто
// переносится на сейчас, и воркер придёт на ближайшей проверке.
func (s *Server) gameBuildNow(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	if err := s.builds.Hurry(r.Context(), gameID, time.Now()); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/games/"+gameID, http.StatusSeeOther)
}

// buildBack отдаёт обновлённую очередь. С HTMX это кусок страницы — карта
// и форма добавления остаются на месте, а они собраны из состояния партии,
// и заново его читать было бы дорого. Без HTMX возвращаемся на страницу
// партии; карты там уже не будет, но очередь видна и без неё.
func (s *Server) buildBack(w http.ResponseWriter, r *http.Request, gameID, errMsg string) {
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/games/"+gameID, http.StatusSeeOther)
		return
	}
	data, err := s.buildData(r, gameID, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["GameID"] = gameID
	data["BuildError"] = errMsg
	data["CSRFToken"] = csrfToken(r)
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderPartialStatus(w, r, status, "game", "buildqueue", data)
}

func (s *Server) buildFailed(w http.ResponseWriter, r *http.Request, gameID, msg string) {
	s.buildBack(w, r, gameID, msg)
}

// idAndName разбирает поле формы вида «16|Крепость». Номер и название
// приходят вместе потому, что выбор собран из состояния партии, а хранить
// в очереди только номер нельзя: названия нужны странице, которая в партию
// не заходила.
func idAndName(value string) (int, string, bool) {
	number, name, ok := strings.Cut(value, "|")
	if !ok {
		return 0, "", false
	}
	id, err := strconv.Atoi(number)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > buildNameMax {
		return 0, "", false
	}
	return id, name, true
}
