package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

var traitCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

func (s *Server) traitsList(w http.ResponseWriter, r *http.Request) {
	s.renderTraits(w, r, http.StatusOK, "")
}

func (s *Server) renderTraits(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
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

	// Те же суммы, что и в знаменателе шкал: видно цену каждого признака.
	var riskMax, loyaltyMax int
	for _, t := range traits {
		if !t.IsActive {
			continue
		}
		if t.IsNegative() {
			riskMax += -t.Weight
		} else {
			loyaltyMax += t.Weight
		}
	}

	s.render(w, r, status, "traits", map[string]any{
		"Traits": traits, "Usage": usage, "Error": errMsg,
		"RiskMax": riskMax, "LoyaltyMax": loyaltyMax,
	})
}

func (s *Server) traitCreate(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSpace(r.PostFormValue("code")))
	name := strings.TrimSpace(r.PostFormValue("name"))
	weight, weightErr := strconv.Atoi(strings.TrimSpace(r.PostFormValue("weight")))
	sortOrder, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("sort_order")))
	if err != nil {
		sortOrder = 100
	}

	switch {
	case !traitCodeRe.MatchString(code):
		s.renderTraits(w, r, http.StatusUnprocessableEntity,
			"Код: латиница в нижнем регистре, цифры и подчёркивание, от 2 до 40 символов")
		return
	case name == "" || len([]rune(name)) > 80:
		s.renderTraits(w, r, http.StatusUnprocessableEntity, "Укажите название не длиннее 80 символов")
		return
	case weightErr != nil || weight < -100 || weight > 100:
		s.renderTraits(w, r, http.StatusUnprocessableEntity, "Вес — целое число от -100 до 100")
		return
	}

	trait, err := s.traits.Create(r.Context(), code, name, weight, sortOrder)
	if errors.Is(err, domain.ErrCodeTaken) {
		s.renderTraits(w, r, http.StatusUnprocessableEntity, "Признак с таким кодом уже есть")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditOn(r, "trait.create", "trait", trait.ID,
		map[string]any{"code": code, "name": name, "weight": weight})
	http.Redirect(w, r, "/traits", http.StatusSeeOther)
}

func (s *Server) traitUpdate(w http.ResponseWriter, r *http.Request) {
	trait, ok := s.loadTrait(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	weight, weightErr := strconv.Atoi(strings.TrimSpace(r.PostFormValue("weight")))
	sortOrder, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("sort_order")))
	if err != nil {
		sortOrder = trait.SortOrder
	}
	isActive := r.PostFormValue("is_active") == "true"

	switch {
	case name == "" || len([]rune(name)) > 80:
		s.renderTraits(w, r, http.StatusUnprocessableEntity, "Укажите название не длиннее 80 символов")
		return
	case weightErr != nil || weight < -100 || weight > 100:
		s.renderTraits(w, r, http.StatusUnprocessableEntity, "Вес — целое число от -100 до 100")
		return
	}

	if err := s.traits.Update(r.Context(), trait.ID, name, weight, sortOrder, isActive); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Веса — это политика оценки, поэтому фиксируем прежнее значение.
	s.logAuditOn(r, "trait.update", "trait", trait.ID, map[string]any{
		"code": trait.Code, "name": name,
		"weight_from": trait.Weight, "weight_to": weight,
		"active_from": trait.IsActive, "active_to": isActive,
	})
	http.Redirect(w, r, "/traits", http.StatusSeeOther)
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
		map[string]any{"code": trait.Code, "name": trait.Name, "weight": trait.Weight})
	http.Redirect(w, r, "/traits", http.StatusSeeOther)
}

func (s *Server) loadTrait(w http.ResponseWriter, r *http.Request) (domain.Trait, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return domain.Trait{}, false
	}
	trait, err := s.traits.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Признак не найден", http.StatusNotFound)
		return domain.Trait{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return domain.Trait{}, false
	}
	return trait, true
}
