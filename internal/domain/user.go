package domain

import (
	"regexp"
	"strings"
	"time"
)

// Role определяет уровень доступа. Порядок важен: чем больше вес, тем больше прав.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
	RoleRoot  Role = "root"
)

var roleWeight = map[Role]int{RoleUser: 1, RoleAdmin: 2, RoleRoot: 3}

func (r Role) Valid() bool { _, ok := roleWeight[r]; return ok }

// AtLeast сообщает, что роль не ниже требуемой.
func (r Role) AtLeast(min Role) bool { return roleWeight[r] >= roleWeight[min] }

// TitleKey — ключ подписи к роли. Незнакомую роль подписываем её же кодом:
// в базе таких нет, но выдумывать ей название хуже, чем показать как есть.
func (r Role) TitleKey() string {
	if _, ok := roleWeight[r]; ok {
		return "role." + string(r)
	}
	return string(r)
}

type User struct {
	ID       int64
	Email    string // необязателен: пустая строка — почта не задана
	Nickname string
	// Профиль: кто это и под каким ником он играет. Всё необязательное —
	// доступ выдают раньше, чем человек дойдёт до своей страницы.
	FullName     string
	City         string
	GameID       string
	PasswordHash string
	Role         Role
	IsActive     bool
	// GamesAccess — персональный пропуск в раздел «Игры». Выдаёт его только
	// рут: раздел ходит в Supremacy под общим аккаунтом проекта, и раздавать
	// его по роли админа мы не хотим. Рут проходит и без флага.
	GamesAccess bool
	// MapChecks — сколько раз в сутки человек может посмотреть карту партии.
	// Каждый показ карты — проверка, даже если данные пришли из общего кэша.
	// Ноль означает запрет; рут вне счёта, его число ни на что не влияет.
	MapChecks int
	CreatedBy *int64
	CreatedAt time.Time
}

// Хелперы для шаблонов: html/template не умеет передавать строковый
// литерал в аргумент типа Role, поэтому проверки роли оформлены методами.
func (u *User) IsRoot() bool  { return u.Role == RoleRoot }
func (u *User) IsAdmin() bool { return u.Role.AtLeast(RoleAdmin) }

// CanViewGames — пускать ли в раздел «Игры». Право персональное, а не
// ступень в лестнице ролей: админ без выданного флага раздела не видит.
func (u *User) CanViewGames() bool { return u.IsRoot() || u.GamesAccess }

// Display — как подписывать пользователя там, где место на одну строку:
// в журнале, в авторе заметки. Почта информативнее, но её может не быть.
func (u *User) Display() string {
	if u.Email != "" {
		return u.Email
	}
	return u.Nickname
}

// Ник заменяет почту при входе, поэтому он обязателен и уникален.
// Разрешаем буквы (в том числе кириллицу), цифры, точку, дефис и подчёркивание;
// пробелы и «собаку» исключаем, чтобы ник нельзя было спутать с адресом.
var nicknameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]{2,31}$`)

func ValidateNickname(nick string) error {
	if nick == "" {
		return ErrNickRequired
	}
	if !nicknameRe.MatchString(nick) {
		return ErrNickFormat
	}
	return nil
}

// ValidateProfile проверяет то, что человек пишет о себе сам. Ограничения
// мягкие: это справка для своих, а не документ.
func ValidateProfile(fullName, city, gameID string) error {
	if len([]rune(fullName)) > 100 {
		return ErrNameTooLong
	}
	if len([]rune(city)) > 100 {
		return ErrCityTooLong
	}
	if len([]rune(gameID)) > 32 {
		return ErrGameIDTooLong
	}
	if strings.ContainsAny(gameID, " \t\n") {
		return ErrGameIDSpaces
	}
	return nil
}

// MsgError — ошибка, которую увидит человек. Носит не текст, а ключ
// сообщения: язык страницы выбирает тот, кто смотрит, и предметная
// область не должна решать этот вопрос за него.
//
// Error() отдаёт ключ: в лог он попадёт как «err.nick.taken» — читаемо
// и однозначно, а на страницу его переводит веб-слой.
type MsgError struct{ Key string }

func (e *MsgError) Error() string { return e.Key }

var (
	ErrNotFound      = &MsgError{"err.notfound"}
	ErrEmailTaken    = &MsgError{"err.email.taken"}
	ErrNickTaken     = &MsgError{"err.nick.taken"}
	ErrGameIDTaken   = &MsgError{"err.gameid.taken"}
	ErrCodeTaken     = &MsgError{"err.code.taken"}
	ErrClanTaken     = &MsgError{"err.clan.taken"}
	ErrAlreadyListed = &MsgError{"err.already.listed"}
	ErrForbidden     = &MsgError{"err.forbidden"}
	ErrInvalidLogin  = &MsgError{"err.invalid.login"}

	ErrNickRequired  = &MsgError{"err.nick.required"}
	ErrNickFormat    = &MsgError{"err.nick.format"}
	ErrNameTooLong   = &MsgError{"err.name.toolong"}
	ErrCityTooLong   = &MsgError{"err.city.toolong"}
	ErrGameIDTooLong = &MsgError{"err.gameid.toolong"}
	ErrGameIDSpaces  = &MsgError{"err.gameid.spaces"}

	ErrClanNameRequired = &MsgError{"err.clan.name.required"}
	ErrClanNameTooLong  = &MsgError{"err.clan.name.toolong"}

	ErrPlayerNickRequired = &MsgError{"err.player.nick.required"}
	ErrPlayerNickTooLong  = &MsgError{"err.player.nick.toolong"}
	ErrPlayerIDRequired   = &MsgError{"err.player.id.required"}
	ErrPlayerIDTooLong    = &MsgError{"err.player.id.toolong"}
	ErrPlayerIDSpaces     = &MsgError{"err.player.id.spaces"}

	ErrPasswordShort = &MsgError{"err.password.short"}
	ErrPasswordLong  = &MsgError{"err.password.long"}
)

// CanManage описывает, кто кого вправе изменять: рут — всех кроме себя,
// админ — обычных пользователей и других админов, но не рута и не себя.
func CanManage(actor, target *User) bool {
	if actor.ID == target.ID || target.Role == RoleRoot {
		return false
	}
	return actor.Role.AtLeast(RoleAdmin)
}
