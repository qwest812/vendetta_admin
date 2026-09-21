package domain

import "time"

// HuntTarget — игрок, которого рут ищет в лобби: как только он окажется
// в открытой партии, в телеграм уходит сообщение. Ищем по номеру на сайте:
// ник игрок может сменить, номер — нет.
type HuntTarget struct {
	ID         int64
	SiteUserID string
	// Nickname — как его звать в сообщении и в списке. Обновляется тем,
	// что пришло из лобби: там ник всегда нынешний.
	Nickname string
	AddedAt  time.Time
}

// HuntHit — где игрока нашли. О каждой паре «игрок + партия» сообщаем
// один раз, поэтому находка и есть отметка «уже сказали».
type HuntHit struct {
	TargetID   int64
	SiteUserID string
	Nickname   string
	GameID     string
	Title      string
	// Nation — за какую страну он там. Пусто, если игра не сказала.
	Nation  string
	FoundAt time.Time
}
