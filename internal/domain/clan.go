package domain

import (
	"strings"
	"time"
)

// ClanStatus — как мы относимся к клану целиком: союзный, нейтральный или
// враждебный. Статус — свойство клана, а не игрока: помеченный альянс красит
// все свои карточки сразу.
//
// На шкалы риска и лояльности статус не влияет. Это разные вещи: шкалы — про
// репутацию человека, статус — про политику его альянса.
type ClanStatus string

const (
	ClanAlly    ClanStatus = "ally"
	ClanNeutral ClanStatus = "neutral"
	ClanEnemy   ClanStatus = "enemy"
)

// ClanStatuses — порядок для выпадающих списков: от своих к чужим.
var ClanStatuses = []ClanStatus{ClanAlly, ClanNeutral, ClanEnemy}

var clanStatusTitles = map[ClanStatus]string{
	ClanAlly:    "Союзный",
	ClanNeutral: "Нейтральный",
	ClanEnemy:   "Враждебный",
}

// ParseClanStatus разбирает значение из формы или строки запроса. Второе
// значение false — это либо мусор, либо пусто: у фильтра поиска пусто значит
// «все кланы», и решает это уже вызывающий.
func ParseClanStatus(s string) (ClanStatus, bool) {
	status := ClanStatus(strings.TrimSpace(s))
	_, ok := clanStatusTitles[status]
	return status, ok
}

func (s ClanStatus) Valid() bool { return clanStatusTitles[s] != "" }

func (s ClanStatus) Title() string {
	if title, ok := clanStatusTitles[s]; ok {
		return title
	}
	return "Нейтральный"
}

func (s ClanStatus) IsAlly() bool  { return s == ClanAlly }
func (s ClanStatus) IsEnemy() bool { return s == ClanEnemy }

// Marked — статус, который стоит показывать отдельной меткой. Нейтральные
// кланы — это большинство, для них метка была бы шумом.
func (s ClanStatus) Marked() bool { return s.IsAlly() || s.IsEnemy() }

type Clan struct {
	ID        int64
	Name      string
	Status    ClanStatus
	Players   int // сколько карточек числится за кланом
	CreatedAt time.Time
}
