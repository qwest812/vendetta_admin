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

// BuildEntry — запись очереди строительства: какое здание и в какой
// провинции ставить. Названия хранятся рядом с номерами, чтобы показывать
// очередь, не заходя в партию.
type BuildEntry struct {
	ID         int64
	GameID     string
	ProvinceID int
	Province   string
	UpgradeID  int
	Upgrade    string
	Position   int
	AddedAt    time.Time
}
