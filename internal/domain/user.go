package domain

import (
	"errors"
	"regexp"
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

func (r Role) Title() string {
	switch r {
	case RoleRoot:
		return "Рут"
	case RoleAdmin:
		return "Администратор"
	case RoleUser:
		return "Пользователь"
	}
	return string(r)
}

type User struct {
	ID           int64
	Email        string // необязателен: пустая строка — почта не задана
	Nickname     string
	PasswordHash string
	Role         Role
	IsActive     bool
	CreatedBy    *int64
	CreatedAt    time.Time
}

// Хелперы для шаблонов: html/template не умеет передавать строковый
// литерал в аргумент типа Role, поэтому проверки роли оформлены методами.
func (u *User) IsRoot() bool  { return u.Role == RoleRoot }
func (u *User) IsAdmin() bool { return u.Role.AtLeast(RoleAdmin) }

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
		return errors.New("ник обязателен")
	}
	if !nicknameRe.MatchString(nick) {
		return errors.New("ник: 3–32 символа, буквы, цифры, точка, дефис и подчёркивание; начинается с буквы или цифры")
	}
	return nil
}

var (
	ErrNotFound     = errors.New("не найдено")
	ErrEmailTaken   = errors.New("почта уже используется")
	ErrNickTaken    = errors.New("такой ник уже есть в базе")
	ErrGameIDTaken  = errors.New("такой игровой ID уже есть в базе")
	ErrCodeTaken    = errors.New("такой код признака уже есть")
	ErrClanTaken    = errors.New("клан с таким названием уже есть")
	ErrForbidden    = errors.New("недостаточно прав")
	ErrInvalidLogin = errors.New("неверная почта или пароль")
)

// CanManage описывает, кто кого вправе изменять: рут — всех кроме себя,
// админ — обычных пользователей и других админов, но не рута и не себя.
func CanManage(actor, target *User) bool {
	if actor.ID == target.ID || target.Role == RoleRoot {
		return false
	}
	return actor.Role.AtLeast(RoleAdmin)
}
