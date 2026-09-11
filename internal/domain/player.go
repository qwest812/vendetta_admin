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

// TraitKinds — все знаки в том порядке, в каком их показывает форма:
// от плохого к хорошему.
var TraitKinds = []TraitKind{TraitBad, TraitNeutral, TraitGood}

// TitleKey — ключ подписи к знаку: словами это скажет интерфейс, и на том
// языке, который выбрал смотрящий.
func (k TraitKind) TitleKey() string { return "traitkind." + string(k) }

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

// MarkedTraits — отмеченные признаки набором по номеру. Нужен карточке:
// переключатели рисуются по всему справочнику, и у каждого надо знать,
// нажат он или нет.
func (p Player) MarkedTraits() map[int64]bool {
	out := make(map[int64]bool, len(p.Traits))
	for _, t := range p.Traits {
		out[t.ID] = true
	}
	return out
}

// CommentMaxLen — насколько длинным бывает комментарий. Пятьсот символов
// хватает, чтобы рассказать случай, и мало, чтобы устроить в карточке
// переписку: разговоры ведут в обратной связи, а здесь копят наблюдения.
const CommentMaxLen = 500

// Comment — что человек написал об игроке. У каждого он один на игрока:
// новый текст заменяет прежний и всплывает наверх ленты, поэтому у записи
// две даты — когда написали впервые и когда переписали.
//
// В ленте комментарии анонимны: автора видят только админы. Имя здесь
// поэтому и лежит отдельным полем — интерфейс решает, показывать его или
// промолчать.
type Comment struct {
	ID       int64
	PlayerID int64
	// AuthorID пуст, если автора удалили. Сам комментарий при этом остаётся:
	// сказанное об игроке не должно пропадать вместе с учётной записью.
	AuthorID   *int64
	AuthorName string
	Body       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Edited — переписывали ли комментарий после того, как написали.
func (c Comment) Edited() bool { return c.UpdatedAt.After(c.CreatedAt) }

// Mine — мой ли это комментарий. По нему карточка решает, показывать ли
// кнопку удаления и предупреждать ли, что новый текст заменит прежний.
func (c Comment) Mine(actor *User) bool {
	return actor != nil && c.AuthorID != nil && *c.AuthorID == actor.ID
}

// CanDelete: свой комментарий убирает автор, чужой — админ и выше.
// Осиротевший (автора удалили) остаётся админам.
func (c Comment) CanDelete(actor *User) bool {
	if actor == nil {
		return false
	}
	if c.Mine(actor) {
		return true
	}
	return actor.IsAdmin()
}

// TraitMark — признак в карточке: сам признак и сколько людей его отметили.
// Одна отметка и пять отметок — разные вещи, и число рядом с названием
// говорит об игроке больше, чем сама метка.
type TraitMark struct {
	Trait
	// Count — сколько человек отметили признак у этого игрока.
	Count int
	// Mine — отмечал ли его я. По нему стоит галочка в форме.
	Mine bool
	// By — кто отметил. Заполняется только для админов: остальным лента
	// и счётчики показываются без имён.
	By []string
}
