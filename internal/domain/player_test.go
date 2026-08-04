package domain

import "testing"

func traits() []Trait {
	return []Trait{
		{ID: 1, Code: "multiaccount", Weight: -10, IsActive: true},
		{ID: 2, Code: "breaks_agreements", Weight: -8, IsActive: true},
		{ID: 3, Code: "foul_language", Weight: -4, IsActive: true},
		{ID: 4, Code: "plays_well", Weight: 6, IsActive: true},
		{ID: 5, Code: "donator", Weight: 3, IsActive: true},
		{ID: 6, Code: "night_player", Weight: 0, IsActive: true},
	}
}

func pick(all []Trait, ids ...int64) []Trait {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Trait
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

func TestComputeScore(t *testing.T) {
	all := traits() // риск: 10+8+4 = 22, лояльность: 6+3 = 9

	cases := []struct {
		name        string
		ids         []int64
		risk        int
		loyalty     int
		wantLevel   string
		wantRiskCls string
	}{
		{"без отметок", nil, 0, 0, "чисто", "none"},
		{"только нейтральный", []int64{6}, 0, 0, "чисто", "none"},
		{"мультиаккаунт", []int64{1}, 45, 0, "средний", "mid"},
		{"все минусы", []int64{1, 2, 3}, 100, 0, "высокий", "high"},
		{"все плюсы", []int64{4, 5}, 0, 100, "чисто", "none"},
		{"сквернословит и хорошо играет", []int64{3, 4}, 18, 67, "низкий", "low"},
		{"всё сразу", []int64{1, 2, 3, 4, 5, 6}, 100, 100, "высокий", "high"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ComputeScore(all, pick(all, c.ids...))
			if s.Risk != c.risk {
				t.Errorf("Risk = %d, ожидалось %d", s.Risk, c.risk)
			}
			if s.Loyalty != c.loyalty {
				t.Errorf("Loyalty = %d, ожидалось %d", s.Loyalty, c.loyalty)
			}
			if got := s.RiskLevel(); got != c.wantLevel {
				t.Errorf("RiskLevel = %q, ожидалось %q", got, c.wantLevel)
			}
			if got := s.RiskClass(); got != c.wantRiskCls {
				t.Errorf("RiskClass = %q, ожидалось %q", got, c.wantRiskCls)
			}
		})
	}
}

// Неактивный признак выпадает и из знаменателя, и из набранных очков:
// иначе отключение признака задним числом раздувало бы риск у всех.
func TestComputeScoreIgnoresInactive(t *testing.T) {
	all := traits()
	all[2].IsActive = false // foul_language, вес -4 => риск теперь из 18

	s := ComputeScore(all, pick(all, 1))
	if s.RiskMax != 18 {
		t.Errorf("RiskMax = %d, ожидалось 18", s.RiskMax)
	}
	if s.Risk != 56 {
		t.Errorf("Risk = %d, ожидалось 56", s.Risk)
	}

	// Отметка на выключенном признаке не даёт очков.
	withDisabled := ComputeScore(all, pick(all, 1, 3))
	if withDisabled.RiskPoints != 10 {
		t.Errorf("RiskPoints = %d, ожидалось 10", withDisabled.RiskPoints)
	}
}

// Пустой справочник не должен приводить к делению на ноль.
func TestComputeScoreEmptyRegistry(t *testing.T) {
	s := ComputeScore(nil, nil)
	if s.Risk != 0 || s.Loyalty != 0 {
		t.Errorf("ожидались нули, получено risk=%d loyalty=%d", s.Risk, s.Loyalty)
	}
	if got := s.RiskLevel(); got != "не задан" {
		t.Errorf("RiskLevel = %q, ожидалось \"не задан\"", got)
	}
}

// Веса меняются на ходу — оценка обязана следовать за справочником.
func TestScoreFollowsWeightChange(t *testing.T) {
	all := traits()
	before := ComputeScore(all, pick(all, 3)) // -4 из 22 => 18%

	all[0].Weight = -2                       // мультиаккаунт подешевел: знаменатель 2+8+4 = 14
	after := ComputeScore(all, pick(all, 3)) // -4 из 14 => 29%

	if before.Risk != 18 || after.Risk != 29 {
		t.Errorf("ожидалось 18%% до и 29%% после, получено %d%% и %d%%", before.Risk, after.Risk)
	}
}

// Заметку удаляет её автор независимо от роли; чужую — админ и выше.
func TestNoteCanDelete(t *testing.T) {
	author := &User{ID: 4, Role: RoleUser}
	stranger := &User{ID: 5, Role: RoleUser}
	admin := &User{ID: 6, Role: RoleAdmin}
	root := &User{ID: 1, Role: RoleRoot}

	authorID := author.ID
	note := Note{ID: 7, AuthorID: &authorID}

	if !note.CanDelete(author) {
		t.Error("автор должен удалять свою заметку")
	}
	if note.CanDelete(stranger) {
		t.Error("обычный пользователь не должен удалять чужую заметку")
	}
	if !note.CanDelete(admin) {
		t.Error("админ должен удалять чужую заметку")
	}
	if !note.CanDelete(root) {
		t.Error("рут должен удалять любую заметку")
	}

	orphan := Note{ID: 8}
	if orphan.CanDelete(stranger) || !orphan.CanDelete(admin) {
		t.Error("заметку без автора убирает админ, но не обычный пользователь")
	}
}
