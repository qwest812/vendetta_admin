package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

const (
	CookieName = "vendetta_session"
	tokenBytes = 32
)

type Service struct {
	users    *repo.Users
	sessions *repo.Sessions
	ttl      time.Duration
	secure   bool
}

func NewService(users *repo.Users, sessions *repo.Sessions, ttl time.Duration, secure bool) *Service {
	return &Service{users: users, sessions: sessions, ttl: ttl, secure: secure}
}

// Login проверяет учётные данные и заводит сессию, выставляя cookie.
func (s *Service) Login(ctx context.Context, w http.ResponseWriter, email, password string) (*domain.User, error) {
	user, err := s.users.ByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		// Считаем хеш и на несуществующей почте, чтобы время ответа не
		// выдавало, зарегистрирован адрес или нет.
		_ = VerifyPassword(password, dummyHash)
		return nil, domain.ErrInvalidLogin
	}
	if err != nil {
		return nil, err
	}
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return nil, domain.ErrInvalidLogin
	}
	if !user.IsActive {
		return nil, domain.ErrInvalidLogin
	}

	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}

	expires := time.Now().Add(s.ttl)
	sum := hashToken(token)
	if err := s.sessions.Create(ctx, sum[:], user.ID, csrf, expires); err != nil {
		return nil, err
	}
	s.setCookie(w, token, expires)
	return user, nil
}

func (s *Service) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		sum := hashToken(c.Value)
		if err := s.sessions.Delete(ctx, sum[:]); err != nil {
			return err
		}
	}
	s.setCookie(w, "", time.Unix(0, 0))
	return nil
}

// Current возвращает сессию по cookie либо domain.ErrNotFound.
func (s *Service) Current(ctx context.Context, r *http.Request) (*repo.Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, domain.ErrNotFound
	}
	sum := hashToken(c.Value)
	sess, err := s.sessions.Lookup(ctx, sum[:])
	if err != nil {
		return nil, err
	}
	// Продлеваем скользящее окно, когда истекла половина срока.
	if time.Until(sess.ExpiresAt) < s.ttl/2 {
		_ = s.sessions.Touch(ctx, sum[:], time.Now().Add(s.ttl))
	}
	return sess, nil
}

func (s *Service) setCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// Заглушка для выравнивания времени ответа на несуществующей почте.
var dummyHash, _ = HashPassword("dummy-password-for-timing")
