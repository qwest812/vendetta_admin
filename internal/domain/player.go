package domain

import (
	"math"
	"time"
)

// Trait — признак из справочника. Вес со знаком: отрицательный работает на
// шкалу риска, положительный — на шкалу лояльности, нулевой ни на что не
// влияет и остаётся просто пометкой.
type Trait struct {
	ID        int64
	Code      string
	Name      string
	Weight    int
	IsActive  bool
	SortOrder int
	CreatedAt time.Time
}

func (t Trait) IsNegative() bool { return t.Weight < 0 }
func (t Trait) IsPositive() bool { return t.Weight > 0 }
func (t Trait) IsNeutral() bool  { return t.Weight == 0 }

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
	Score  Score
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

// Score — две независимые шкалы. Риск считается по отрицательным признакам,
// лояльность по положительным; каждая нормируется на сумму весов всех
// активных признаков своего знака. Поэтому добавление нового признака в
// справочник сдвигает оценки всех игроков — это осознанно: шкала всегда
// означает «сколько из возможного набрано».
type Score struct {
	Risk    int // 0..100
	Loyalty int // 0..100

	RiskPoints    int // набрано по модулю
	RiskMax       int
	LoyaltyPoints int
	LoyaltyMax    int
}

// RiskLevel даёт словесную оценку риска для карточки и списка.
func (s Score) RiskLevel() string {
	switch {
	case s.RiskMax == 0:
		return "не задан"
	case s.Risk >= 70:
		return "высокий"
	case s.Risk >= 40:
		return "средний"
	case s.Risk > 0:
		return "низкий"
	}
	return "чисто"
}

// RiskClass — CSS-класс для окраски шкалы.
func (s Score) RiskClass() string {
	switch {
	case s.Risk >= 70:
		return "high"
	case s.Risk >= 40:
		return "mid"
	case s.Risk > 0:
		return "low"
	}
	return "none"
}

// ComputeScore считает шкалы. all — весь активный справочник,
// selected — признаки, отмеченные у игрока.
func ComputeScore(all []Trait, selected []Trait) Score {
	var s Score
	for _, t := range all {
		if !t.IsActive {
			continue
		}
		if t.IsNegative() {
			s.RiskMax += -t.Weight
		} else {
			s.LoyaltyMax += t.Weight
		}
	}
	for _, t := range selected {
		if !t.IsActive {
			continue
		}
		if t.IsNegative() {
			s.RiskPoints += -t.Weight
		} else {
			s.LoyaltyPoints += t.Weight
		}
	}
	s.Risk = percent(s.RiskPoints, s.RiskMax)
	s.Loyalty = percent(s.LoyaltyPoints, s.LoyaltyMax)
	return s
}

func percent(part, total int) int {
	if total <= 0 {
		return 0
	}
	p := int(math.Round(float64(part) / float64(total) * 100))
	return min(max(p, 0), 100)
}
