package domain

import "time"

// GameTask — что админка делает в партии Supremacy сама. Настройка живёт на
// партии, а не на пользователе: игровой аккаунт в проекте один, и раздел
// «Игры» доступен только руту.
type GameTask struct {
	GameID string
	Title  string
	// HeroDeploy — жать ли по таймеру навык Мейв «призвать пехоту».
	HeroDeploy bool
	UpdatedBy  *int64
	UpdatedAt  time.Time
	// LastRunAt и LastResult — чем кончился последний заход воркера.
	// Нулевое время означает «ни разу не ходили».
	LastRunAt  time.Time
	LastResult string
	// BuildNextAt, BuildRunAt и BuildResult — то же самое про очередь
	// строительства. Время следующего захода считает сама партия: к концу
	// стройки или к накоплению ресурсов, см. supremacy.PlanBuilds.
	BuildNextAt time.Time
	BuildRunAt  time.Time
	BuildResult string
}

// Виды очереди. Здания и войска — два списка на провинцию: у игры у них
// разные слоты (стройка и производство), и одно другого не ждёт.
const (
	QueueBuilding = "building"
	QueueUnit     = "unit"
)

// BuildEntry — запись очереди: какое здание или войско и в какой провинции
// ставить. Названия и ключ картинки хранятся рядом с номерами, чтобы
// показывать очередь, не заходя в партию.
type BuildEntry struct {
	ID         int64
	GameID     string
	Kind       string // QueueBuilding или QueueUnit
	ProvinceID int
	Province   string
	// UpgradeID — номер здания, а у войска — номер типа юнита.
	UpgradeID int
	Upgrade   string
	// Image — имя картинки у игры: «railway» у здания, «car» у войска.
	Image    string
	Position int
	AddedAt  time.Time
}

// BuildQueues — очередь партии в том виде, в котором её читает воркер:
// номера по порядку на каждую провинцию, отдельно здания и войска.
type BuildQueues struct {
	Buildings map[int][]int
	Units     map[int][]int
}

// Empty — нечего делать ни в одной провинции.
func (q BuildQueues) Empty() bool { return len(q.Buildings) == 0 && len(q.Units) == 0 }
