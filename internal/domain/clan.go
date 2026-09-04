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

// clanStatusKeys — ключи подписей к статусам. Сам текст живёт в словаре
// интерфейса: подпись зависит от языка смотрящего, а статус — нет.
var clanStatusKeys = map[ClanStatus]string{
	ClanAlly:    "clanstatus.ally",
	ClanNeutral: "clanstatus.neutral",
	ClanEnemy:   "clanstatus.enemy",
}

// ParseClanStatus разбирает значение из формы или строки запроса. Второе
// значение false — это либо мусор, либо пусто: у фильтра поиска пусто значит
// «все кланы», и решает это уже вызывающий.
func ParseClanStatus(s string) (ClanStatus, bool) {
	status := ClanStatus(strings.TrimSpace(s))
	_, ok := clanStatusKeys[status]
	return status, ok
}

func (s ClanStatus) Valid() bool { return clanStatusKeys[s] != "" }

// TitleKey — ключ подписи к статусу. Пустой статус приходит с игроком
// без клана, и подписать его надо словом «нейтральный», а не пустотой:
// в интерфейсе это одно и то же — «ничего особенного про этот клан».
func (s ClanStatus) TitleKey() string {
	if key, ok := clanStatusKeys[s]; ok {
		return key
	}
	return "clanstatus.neutral"
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
