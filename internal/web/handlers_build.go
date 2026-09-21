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
	Image string // имя картинки у игры
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
		out = append(out, buildUpgrade{ID: u.ID, Label: label, Image: u.Image})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// unitProvince — своя провинция в форме заказа войск: что производится
// сейчас и сколько войск она вообще может производить. Ноль — нужных
// зданий нет, и заказ туда будет ждать, пока они появятся.
type unitProvince struct {
	ID    int
	Name  string
	Now   string
	Ends  time.Time
	Queue int
	Can   int
}

// unitProvinces — наши провинции для заказа войск, по названию.
func unitProvinces(state *supremacy.GameState, entries []domain.BuildEntry) []unitProvince {
	if state == nil || state.Me <= 0 {
		return nil
	}
	queued := map[int]int{}
	for _, e := range entries {
		queued[e.ProvinceID]++
	}
	var out []unitProvince
	for _, p := range state.Provinces {
		if p.Owner != state.Me {
			continue
		}
		view := unitProvince{ID: p.ID, Name: p.Name, Queue: queued[p.ID], Can: len(p.CanProduce)}
		if ends := p.ProducesUntil(); !ends.IsZero() {
			view.Ends = ends
			for _, c := range p.Producing {
				if c.Ends.Equal(ends) {
					view.Now = state.Units[c.UnitTypeID].Name
				}
			}
			if view.Now == "" {
				view.Now = "войско"
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// unitChoices — войска, которые можно заказать в этой партии: сначала
// суша, потом воздух и море, внутри — по названию.
func unitChoices(state *supremacy.GameState) []buildUpgrade {
	if state == nil {
		return nil
	}
	type choice struct {
		buildUpgrade
		set int
	}
	var all []choice
	for _, u := range state.Units {
		if u.Name == "" {
			continue
		}
		all = append(all, choice{buildUpgrade{ID: u.ID, Label: u.Name, Image: u.Image}, u.Set})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].set != all[j].set {
			return all[i].set < all[j].set
		}
		return all[i].Label < all[j].Label
	})
	out := make([]buildUpgrade, len(all))
	for i, c := range all {
		out[i] = c.buildUpgrade
	}
	return out
}

// buildData — то, что страница партии знает об очереди. Состояние
// необязательно: без него показывается только сама очередь.
func (s *Server) buildData(r *http.Request, gameID string, state *supremacy.GameState) (map[string]any, error) {
	if s.builds == nil {
		return map[string]any{"BuildQueue": nil, "BuildProvinces": nil, "BuildUpgrades": nil,
			"UnitQueue": nil, "UnitProvinces": nil, "UnitChoices": nil}, nil
	}
	buildings, err := s.builds.List(r.Context(), gameID, domain.QueueBuilding)
	if err != nil {
		return nil, err
	}
	units, err := s.builds.List(r.Context(), gameID, domain.QueueUnit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"BuildQueue":     buildGroups(buildings),
		"BuildProvinces": buildProvinces(state, buildings),
		"BuildUpgrades":  buildUpgrades(state, langOf(r)),
		"UnitQueue":      buildGroups(units),
		"UnitProvinces":  unitProvinces(state, units),
		"UnitChoices":    unitChoices(state),
	}, nil
}

// unitCountMax — сколько войск одного типа можно дописать за раз. Каждое
// становится своей записью: игра производит по одному.
const unitCountMax = 20

// queueKind — вид очереди из формы. Всё незнакомое считается зданием:
// так вели себя формы до появления очереди войск.
func queueKind(r *http.Request) string {
	if r.PostFormValue("kind") == domain.QueueUnit {
		return domain.QueueUnit
	}
	return domain.QueueBuilding
}

// gameBuildAdd дописывает здание в конец очереди провинции.
//
// Номер и название приходят одним полем («16|Крепость»): выбор в форме
// собран из состояния партии, а страница обновляет после этого только
// список очереди — второй раз заходить в партию ради названий нельзя,
// заход стоит входа в игру.
func (s *Server) gameBuildAdd(w http.ResponseWriter, r *http.Request) {
	s.queueAdd(w, r, domain.QueueBuilding)
}

// gameUnitAdd дописывает войска в очередь провинции: столько записей,
// сколько просили, — игра производит по одному.
func (s *Server) gameUnitAdd(w http.ResponseWriter, r *http.Request) {
	s.queueAdd(w, r, domain.QueueUnit)
}

func (s *Server) queueAdd(w http.ResponseWriter, r *http.Request, kind string) {
	gameID := r.PathValue("id")
	provinceID, province, _, okProvince := idAndName(r.PostFormValue("province"))
	upgradeID, upgrade, image, okUpgrade := idAndName(r.PostFormValue("upgrade"))
	count := 1
	if kind == domain.QueueUnit {
		n, err := strconv.Atoi(r.PostFormValue("count"))
		if err != nil || n < 1 || n > unitCountMax {
			okUpgrade = false
		}
		count = n
	}
	if !okProvince || !okUpgrade {
		s.buildBack(w, r, gameID, kind, langOf(r).T("game.build.bad"))
		return
	}

	for range count {
		err := s.builds.Add(r.Context(), domain.BuildEntry{
			GameID: gameID, Kind: kind, ProvinceID: provinceID, Province: province,
			UpgradeID: upgradeID, Upgrade: upgrade, Image: image,
		}, currentUser(r).ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.buildBack(w, r, gameID, kind, "")
}

func (s *Server) gameBuildRemove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		s.buildBack(w, r, gameID, queueKind(r), langOf(r).T("game.build.bad"))
		return
	}
	if err := s.builds.Remove(r.Context(), gameID, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.buildBack(w, r, gameID, queueKind(r), "")
}

// gameBuildMove переставляет запись на шаг вверх или вниз. Крайнюю двигать
// некуда, и это не ошибка: стрелка просто ничего не делает.
func (s *Server) gameBuildMove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err != nil {
		s.buildBack(w, r, gameID, queueKind(r), langOf(r).T("game.build.bad"))
		return
	}
	up := r.PostFormValue("dir") != "down"
	if err := s.builds.Move(r.Context(), gameID, id, up); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.buildBack(w, r, gameID, queueKind(r), "")
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
func (s *Server) buildBack(w http.ResponseWriter, r *http.Request, gameID, kind, errMsg string) {
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
	data["CSRFToken"] = csrfToken(r)
	block := "buildqueue"
	data["BuildError"] = errMsg
	if kind == domain.QueueUnit {
		block = "unitqueue"
		data["UnitError"] = errMsg
		data["BuildError"] = ""
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnprocessableEntity
	}
	s.renderPartialStatus(w, r, status, "game", block, data)
}

// idAndName разбирает поле формы вида «16|Крепость» или «18|Железная
// дорога|railway». Номер, название и имя картинки приходят вместе потому,
// что выбор собран из состояния партии, а хранить в очереди только номер
// нельзя: названия и картинки нужны странице, которая в партию не заходила.
// Картинка необязательна; негодная просто отбрасывается.
func idAndName(value string) (int, string, string, bool) {
	number, rest, ok := strings.Cut(value, "|")
	if !ok {
		return 0, "", "", false
	}
	id, err := strconv.Atoi(number)
	if err != nil || id <= 0 {
		return 0, "", "", false
	}
	name, image, _ := strings.Cut(rest, "|")
	name = strings.TrimSpace(name)
	if len([]rune(name)) > buildNameMax {
		return 0, "", "", false
	}
	if !supremacy.ValidImageKey(image) {
		image = ""
	}
	return id, name, image, true
}
