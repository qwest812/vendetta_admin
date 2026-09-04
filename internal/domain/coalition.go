package domain

import "time"

// Архив коалиций отвечает на один вопрос: эти двое уже союзничали раньше?
// Ответ виден на карте партии до того, как союз сложится заново.
//
// Собирается он заходом наблюдателем, и момент съёма выбран посреди партии
// нарочно: к финалу коалиции распускают, а вместе с ними пропадает состав —
// остаются одни названия.

// WatchedGame — партия под наблюдением архива.
type WatchedGame struct {
	GameID   string
	Title    string
	Language string
	// Speed — во сколько раз партия быстрее обычной: 1, 4, 10. Союз
	// в четырёхдневной скоростной партии и союз в двухмесячной стоят
	// разного, поэтому скорость хранится, а не выводится из названия.
	Speed     float64
	StartedAt time.Time
	State     string
	Day       int

	NextCheckAt time.Time
	CheckedAt   time.Time
	Checks      int
	LastError   string
	Done        bool
}

// Coalition — коалиция вместе с составом на момент, когда мы её застали.
type Coalition struct {
	TeamID  int
	Name    string
	Members []CoalitionMember
}

// CoalitionMember — участник коалиции. Ник и страна нужны, чтобы показать
// пару людьми, а не номерами, когда карточки в базе для них ещё нет.
type CoalitionMember struct {
	SiteUserID string
	Login      string
	Nation     string
}

// CoalitionLink — двое, уже состоявшие в одной коалиции, и в скольких
// партиях одной скорости это было. Пары нигде не хранятся: это результат
// соединения состава с самим собой, посчитанный на лету.
type CoalitionLink struct {
	A, B  string // номера на сайте игры
	Speed float64
	Games int
	// Sample — несколько номеров партий, на них ставятся ссылки.
	// Показывать все незачем: пара, игравшая вместе двадцать раз,
	// и так понятна.
	Sample []string
}

// CoalitionStats — что накопил архив. Показывается руту в разделе «Игры»:
// без счётчика непонятно, работает обход или стоит.
type CoalitionStats struct {
	// Watched — партий под наблюдением, Pending — из них ждут своей
	// проверки, Done — отработанные.
	Watched int
	Pending int
	Done    int
	// Games — в скольких партиях коалиции нашлись, Members — сколько
	// всего записей об участии.
	Games     int
	Members   int
	LastCheck time.Time
}
