package domain

import "time"

// TraitKind — каким признак считается: плохим, нейтральным или хорошим.
// Раньше это говорил знак веса, но вес ушёл вместе со шкалами, а различать
// «мультивод» и «хорошо играет» взглядом по-прежнему нужно. Ни на какие
// расчёты знак не влияет — только на цвет метки и порядок в справочнике.
type TraitKind string

const (
	TraitBad     TraitKind = "bad"
	TraitNeutral TraitKind = "neutral"
	TraitGood    TraitKind = "good"
)

// ParseTraitKind разбирает знак из формы. Второе значение — знаком ли он
// вообще: незнакомое слово это не «нейтральный», а ошибка ввода.
func ParseTraitKind(s string) (TraitKind, bool) {
	switch k := TraitKind(s); k {
	case TraitBad, TraitNeutral, TraitGood:
		return k, true
	}
	return "", false
}

// Trait — признак из справочника: отметка о поведении игрока, по которой
// его потом и ищут.
type Trait struct {
	ID        int64
	Code      string
	Name      string
	Kind      TraitKind
	IsActive  bool
	SortOrder int
	CreatedAt time.Time
}

func (t Trait) IsNegative() bool { return t.Kind == TraitBad }
func (t Trait) IsPositive() bool { return t.Kind == TraitGood }
func (t Trait) IsNeutral() bool  { return t.Kind == TraitNeutral }

type Player struct {
	ID       int64
	GameID   string // ID в игре; постоянен, в отличие от ника. Пусто — не указан
	Nickname string
	ClanID   *int64
	ClanName string
	// ClanStatus пуст, если игрок без клана.
	ClanStatus ClanStatus
	CreatedBy  *int64
	CreatedAt  time.Time
	UpdatedAt  time.Time

	Traits []Trait // отмеченные признаки, отсортированы как в справочнике
}

type Note struct {
	ID          int64
	PlayerID    int64
	AuthorID    *int64
	AuthorEmail string
	Body        string
	CreatedAt   time.Time
}

// CanDelete: свою заметку убирает автор, чужую — админ и выше. Заметка без
// автора (пользователя удалили) остаётся админам.
func (n Note) CanDelete(actor *User) bool {
	if actor == nil {
		return false
	}
	if n.AuthorID != nil && *n.AuthorID == actor.ID {
		return true
	}
	return actor.IsAdmin()
}
