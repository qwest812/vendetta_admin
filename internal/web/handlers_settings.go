package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// mapStatsSpan — за сколько дней показывать открытия карты. Две недели:
// столько живёт обычная партия, и по ним видно и будни, и выходные.
// Хранится счёт дольше (domain.MapChecksTTL), так что окно можно расширить,
// не теряя данных.
const mapStatsSpan = 14 * 24 * time.Hour

// mapsTotal — сколько карт открыли за всё показанное окно.
func mapsTotal(days []repo.MapDay) int {
	n := 0
	for _, d := range days {
		n += d.Total
	}
	return n
}

// Код признака — латиница, цифры и подчёркивание, с буквы. Он идентифицирует
// признак в адресе фильтра и в журнале, поэтому и остаётся неизменяемым:
// переименование кода означало бы, что старая ссылка молча ищет другое.
var traitCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

// settingsPage — общие настройки админки. Раздел рутовый: справочник
// признаков один на всех, и его правка меняет то, что видят остальные.
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, http.StatusOK, "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	// Неактивные тоже нужны: их здесь и включают обратно.
	traits, err := s.traits.List(r.Context(), false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	usage, err := s.traits.UsageCount(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Рубильник сбора коалиций — общий переключатель админки, и место ему
	// здесь. Счётчики архива остались в «Играх»: смотрят на них там.
	var coalitions any
	if s.coalitions != nil && s.settings != nil {
		view, err := s.coalitionCard(r.Context())
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		coalitions = view
	}

	// Открытия карты по дням: сколько их и кем. Читается тот же счётчик,
	// что держит суточный лимит, — отдельного журнала для этого заводить
	// незачем.
	var maps []repo.MapDay
	if s.checks != nil {
		maps, err = s.checks.Daily(r.Context(), repo.Day(time.Now().Add(-mapStatsSpan)))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	s.render(w, r, status, "settings", map[string]any{
		"Traits": traits, "Usage": usage, "Kinds": domain.TraitKinds,
		"Coalitions": coalitions, "Maps": maps, "MapDays": int(mapStatsSpan.Hours() / 24),
		"MapsTotal": mapsTotal(maps), "Error": errMsg,
	})
}

func (s *Server) traitCreate(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSpace(r.PostFormValue("code")))
	name := strings.TrimSpace(r.PostFormValue("name"))
	kind, kindOK := domain.ParseTraitKind(r.PostFormValue("kind"))
	sortOrder, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("sort_order")))
	if err != nil {
		sortOrder = 100
	}

	switch {
	case !traitCodeRe.MatchString(code):
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.trait.code"))
		return
	case name == "" || len([]rune(name)) > 80:
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.trait.name"))
		return
	case !kindOK:
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.trait.kind"))
		return
	}

	trait, err := s.traits.Create(r.Context(), code, name, kind, sortOrder)
	if errors.Is(err, domain.ErrCodeTaken) {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.code.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditOn(r, "trait.create", "trait", trait.ID,
		map[string]any{"code": code, "name": name, "kind": string(kind)})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) traitUpdate(w http.ResponseWriter, r *http.Request) {
	trait, ok := s.loadTrait(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	kind, kindOK := domain.ParseTraitKind(r.PostFormValue("kind"))
	sortOrder, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("sort_order")))
	if err != nil {
		sortOrder = trait.SortOrder
	}
	isActive := r.PostFormValue("is_active") == "true"

	switch {
	case name == "" || len([]rune(name)) > 80:
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.trait.name"))
		return
	case !kindOK:
		s.renderSettings(w, r, http.StatusUnprocessableEntity, langOf(r).T("err.trait.kind"))
		return
	}

	if err := s.traits.Update(r.Context(), trait.ID, name, kind, sortOrder, isActive); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Что было и что стало: справочник общий, и по журналу должно читаться,
	// кто переименовал признак или погасил его.
	s.logAuditOn(r, "trait.update", "trait", trait.ID, map[string]any{
		"code": trait.Code, "name": name,
		"kind_from": string(trait.Kind), "kind_to": string(kind),
		"active_from": trait.IsActive, "active_to": isActive,
	})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) traitDelete(w http.ResponseWriter, r *http.Request) {
	trait, ok := s.loadTrait(w, r)
	if !ok {
		return
	}
	if err := s.traits.Delete(r.Context(), trait.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "trait.delete", "trait", trait.ID,
		map[string]any{"code": trait.Code, "name": trait.Name})
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) loadTrait(w http.ResponseWriter, r *http.Request) (domain.Trait, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, langOf(r).T("err.badid"), http.StatusBadRequest)
		return domain.Trait{}, false
	}
	trait, err := s.traits.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, langOf(r).T("err.trait.notfound"), http.StatusNotFound)
		return domain.Trait{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return domain.Trait{}, false
	}
	return trait, true
}
