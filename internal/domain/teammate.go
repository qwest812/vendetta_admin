package domain

import "time"

// TeammateNoteMax — предел заметки к связи «играют вместе». Хватает
// сказать, откуда известно; больше — это уже комментарий.
const TeammateNoteMax = 200

// Teammate — игрок, который, как отметили руками, играет вместе с тем,
// чью карточку смотрят. Связь взаимная.
type Teammate struct {
	PlayerID int64
	Nickname string
	GameID   string // номер на сайте; пусто — не указан
	Note     string
	AddedBy  string // кто отметил; пусто — автор удалён
	AddedAt  time.Time
}

// TeammatePair — пара «играют вместе» в номерах на сайте: так её
// сравнивают с составом партии.
type TeammatePair struct {
	A, B string
	Note string
}
