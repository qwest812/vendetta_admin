package domain

import "testing"

// Знак признака — слово из формы, и незнакомое слово не должно молча
// становиться нейтральным: так опечатка в справочнике осталась бы незаметной.
func TestParseTraitKind(t *testing.T) {
	for _, s := range []string{"bad", "neutral", "good"} {
		if k, ok := ParseTraitKind(s); !ok || string(k) != s {
			t.Errorf("знак %q не разобран", s)
		}
	}
	for _, s := range []string{"", "BAD", "плохой", "-10"} {
		if _, ok := ParseTraitKind(s); ok {
			t.Errorf("знак %q разобран, а не должен", s)
		}
	}
}

// Цвет метки берётся из знака: три состояния, и ровно одно верно для каждого.
func TestTraitKindPredicates(t *testing.T) {
	for _, c := range []struct {
		kind          TraitKind
		neg, neu, pos bool
	}{
		{TraitBad, true, false, false},
		{TraitNeutral, false, true, false},
		{TraitGood, false, false, true},
	} {
		tr := Trait{Kind: c.kind}
		if tr.IsNegative() != c.neg || tr.IsNeutral() != c.neu || tr.IsPositive() != c.pos {
			t.Errorf("знак %q: минус=%v ноль=%v плюс=%v", c.kind,
				tr.IsNegative(), tr.IsNeutral(), tr.IsPositive())
		}
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
