package domain

import "time"

// Обращение в свободной форме: тема покороче, текст подлиннее. Границы
// нужны обеим сторонам — форме, чтобы не пустить пустое, и базе, чтобы
// в списке не оказалось строки на экран.
const (
	TicketSubjectMin = 3
	TicketSubjectMax = 120
	TicketBodyMin    = 3
	TicketBodyMax    = 4000
)

// TicketStatus — жизнь обращения короткая: оно открыто, пока его не закрыли.
// «В работе» намеренно нет: отвечает один человек, и лишний статус означал бы
// только лишнюю кнопку.
type TicketStatus string

const (
	TicketOpen   TicketStatus = "open"
	TicketClosed TicketStatus = "closed"
)

// ParseTicketStatus разбирает статус из адреса. Неизвестный — это не ошибка,
// а «фильтр не выбран», как и у статусов кланов.
func ParseTicketStatus(s string) (TicketStatus, bool) {
	switch TicketStatus(s) {
	case TicketOpen:
		return TicketOpen, true
	case TicketClosed:
		return TicketClosed, true
	}
	return "", false
}

// Ticket — обращение целиком, без переписки: список показывает именно это.
type Ticket struct {
	ID         int64
	AuthorID   *int64
	AuthorName string
	Subject    string
	Status     TicketStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClosedAt   *time.Time

	// Messages — сколько сообщений в переписке, вместе с первым.
	Messages int
	// HasReply — автору ответили после того, как он последний раз открывал
	// переписку. Считается только для него: админ следит за списком по
	// статусу, а не по прочитанному.
	HasReply bool
}

func (t Ticket) IsOpen() bool { return t.Status == TicketOpen }

// IsAuthor: автор узнаётся по id пользователя, а не по имени — имя в базе
// лежит копией и может устареть.
func (t Ticket) IsAuthor(u *User) bool {
	return u != nil && t.AuthorID != nil && *t.AuthorID == u.ID
}

// CanView — своё обращение видит автор, чужие только админ и выше. Чужое
// обращение постороннему не показывается вовсе: там пишут и о людях.
func (t Ticket) CanView(u *User) bool {
	if u == nil {
		return false
	}
	return t.IsAuthor(u) || u.IsAdmin()
}

// CanClose — закрывает админ или сам автор: «вопрос снят» — это тоже ответ.
func (t Ticket) CanClose(u *User) bool {
	return t.IsOpen() && t.CanView(u)
}

// StatusAfterMessage — что станет с обращением после нового сообщения.
// Закрытое поднимает обратно только автор: если человек пишет снова, вопрос
// не решён. Ответ админа в закрытое обращение его не открывает — так дописывают
// пояснение к уже закрытому.
func (t Ticket) StatusAfterMessage(author *User) TicketStatus {
	if t.IsOpen() {
		return TicketOpen
	}
	if t.IsAuthor(author) {
		return TicketOpen
	}
	return TicketClosed
}

// TicketMessage — одно сообщение переписки.
type TicketMessage struct {
	ID         int64
	TicketID   int64
	AuthorID   *int64
	AuthorName string
	// FromStaff — писал админ. Отмечается при записи: роль автора потом
	// меняется, а сторона, за которую сказано, остаётся прежней.
	FromStaff bool
	Body      string
	CreatedAt time.Time
}
