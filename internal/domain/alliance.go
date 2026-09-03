package domain

import "time"

// Alliance — клан игрока в самой Supremacy: не наш справочник кланов,
// а альянс, в котором человек состоит в игре. Ключ — его номер на сайте
// игры, тот же, что партия отдаёт как siteUserID.
//
// Пустой ID — полноценный ответ «ни в каком клане не состоит», и хранится
// он наравне с остальными: иначе одиночек пришлось бы спрашивать заново
// каждый раз. Отличает «не спрашивали» от «спросили и клана нет» время
// проверки: у неспрошенного его нет.
type Alliance struct {
	SiteUserID string
	ID         string
	Name       string
	Tag        string
	CheckedAt  *time.Time
}

// Known сообщает, что про игрока уже спрашивали: ответ есть, каким бы он
// ни был.
func (a Alliance) Known() bool { return a.CheckedAt != nil }

// InClan — состоит ли игрок в клане. У неспрошенного всегда false: пока
// ответа нет, красить его на карте нечем.
func (a Alliance) InClan() bool { return a.ID != "" }

// GamePlayer — что игра рассказала про игрока, когда мы заходили в партию.
// Только свойства аккаунта: бан принадлежит человеку, а поражение и выход
// из партии — конкретной игре, и в базе им делать нечего.
//
// BannedAt — когда бан увидели впервые; у неснятого бана он и остаётся
// первой встречей, а снятие обнуляет дату вместе с флагом.
type GamePlayer struct {
	SiteUserID string
	Nickname   string
	Banned     bool
	BannedAt   *time.Time
	SeenAt     time.Time
	SeenGameID string
}
