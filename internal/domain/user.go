package domain

import (
	"errors"
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
	Email        string
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

var (
	ErrNotFound     = errors.New("не найдено")
	ErrEmailTaken   = errors.New("почта уже используется")
	ErrNickTaken    = errors.New("такой ник уже есть в базе")
	ErrCodeTaken    = errors.New("такой код признака уже есть")
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
