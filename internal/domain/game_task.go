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
}
