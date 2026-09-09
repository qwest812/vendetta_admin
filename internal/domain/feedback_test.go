package domain

import "testing"

func TestTicketAccess(t *testing.T) {
	author := &User{ID: 2, Role: RoleUser}
	stranger := &User{ID: 3, Role: RoleUser}
	admin := &User{ID: 4, Role: RoleAdmin}
	authorID := author.ID
	ticket := Ticket{ID: 1, AuthorID: &authorID, Status: TicketOpen}

	cases := []struct {
		name        string
		user        *User
		view, close bool
	}{
		{"автор", author, true, true},
		{"админ", admin, true, true},
		// Посторонний не видит обращения вовсе: в них пишут и о людях.
		{"посторонний", stranger, false, false},
		{"не вошёл", nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ticket.CanView(c.user); got != c.view {
				t.Errorf("CanView = %v, ожидалось %v", got, c.view)
			}
			if got := ticket.CanClose(c.user); got != c.close {
				t.Errorf("CanClose = %v, ожидалось %v", got, c.close)
			}
		})
	}

	// Закрытое обращение закрывать нечего — кнопки быть не должно ни у кого.
	closed := ticket
	closed.Status = TicketClosed
	if closed.CanClose(admin) || closed.CanClose(author) {
		t.Error("закрытое обращение предлагается закрыть ещё раз")
	}
	if !closed.CanView(author) {
		t.Error("автор перестал видеть своё закрытое обращение")
	}
}

// Закрытое обращение поднимает только автор: если человек пишет снова, вопрос
// не решён. Приписка админа закрытое не открывает.
func TestTicketStatusAfterMessage(t *testing.T) {
	author := &User{ID: 2, Role: RoleUser}
	admin := &User{ID: 4, Role: RoleAdmin}
	authorID := author.ID

	open := Ticket{AuthorID: &authorID, Status: TicketOpen}
	closed := Ticket{AuthorID: &authorID, Status: TicketClosed}

	cases := []struct {
		name   string
		ticket Ticket
		who    *User
		want   TicketStatus
	}{
		{"открытое, пишет автор", open, author, TicketOpen},
		{"открытое, отвечает админ", open, admin, TicketOpen},
		{"закрытое, пишет автор", closed, author, TicketOpen},
		{"закрытое, дописывает админ", closed, admin, TicketClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.ticket.StatusAfterMessage(c.who); got != c.want {
				t.Errorf("статус = %q, ожидался %q", got, c.want)
			}
		})
	}
}

func TestParseTicketStatus(t *testing.T) {
	if s, ok := ParseTicketStatus("closed"); !ok || s != TicketClosed {
		t.Errorf("closed разобран как %q, ok=%v", s, ok)
	}
	// Мусор в адресе — это «фильтр не выбран», а не ошибка страницы.
	if s, ok := ParseTicketStatus("всё"); ok || s != "" {
		t.Errorf("неизвестный статус разобран как %q, ok=%v", s, ok)
	}
}
