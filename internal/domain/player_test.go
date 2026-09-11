package domain

import (
	"testing"
	"time"
)

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

// Комментарий удаляет его автор независимо от роли; чужой — админ и выше.
func TestCommentCanDelete(t *testing.T) {
	author := &User{ID: 4, Role: RoleUser}
	stranger := &User{ID: 5, Role: RoleUser}
	admin := &User{ID: 6, Role: RoleAdmin}
	root := &User{ID: 1, Role: RoleRoot}

	authorID := author.ID
	comment := Comment{ID: 7, AuthorID: &authorID}

	if !comment.CanDelete(author) {
		t.Error("автор должен удалять свой комментарий")
	}
	if comment.CanDelete(stranger) {
		t.Error("обычный пользователь не должен удалять чужой комментарий")
	}
	if !comment.CanDelete(admin) {
		t.Error("админ должен удалять чужой комментарий")
	}
	if !comment.CanDelete(root) {
		t.Error("рут должен удалять любой комментарий")
	}

	orphan := Comment{ID: 8}
	if orphan.CanDelete(stranger) || !orphan.CanDelete(admin) {
		t.Error("комментарий без автора убирает админ, но не обычный пользователь")
	}
}

// «Свой» комментарий — по автору, а не по роли: рут в чужом комментарии
// не хозяин и предупреждения о замене видеть не должен.
func TestCommentMine(t *testing.T) {
	author := &User{ID: 4, Role: RoleUser}
	root := &User{ID: 1, Role: RoleRoot}
	authorID := author.ID
	comment := Comment{AuthorID: &authorID}

	if !comment.Mine(author) {
		t.Error("автор не узнал свой комментарий")
	}
	if comment.Mine(root) || comment.Mine(nil) {
		t.Error("чужой комментарий сошёл за свой")
	}
	if (Comment{}).Mine(author) {
		t.Error("осиротевший комментарий сошёл за свой")
	}
}

// Переписанный комментарий отличается от написанного: в ленте он всплывает
// наверх, и подпись «изменён» объясняет, почему старая запись оказалась
// выше свежей.
func TestCommentEdited(t *testing.T) {
	at := time.Now()
	if (Comment{CreatedAt: at, UpdatedAt: at}).Edited() {
		t.Error("нетронутый комментарий сочли изменённым")
	}
	if !(Comment{CreatedAt: at, UpdatedAt: at.Add(time.Minute)}).Edited() {
		t.Error("переписанный комментарий сочли нетронутым")
	}
}
