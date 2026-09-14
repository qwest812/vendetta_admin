package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
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
	// logins — журнал входов. Пишется здесь, а не в обработчике: только
	// тут известно, на чей аккаунт пришлась неудачная попытка — снаружи
	// все отказы выглядят одинаково.
	logins *repo.Logins
	// log нужен затем, что ошибку записи в журнал больше некуда деть:
	// вход она прерывать не должна, а пропасть молча не должна тем более.
	log    *slog.Logger
	ttl    time.Duration
	secure bool
}

func NewService(users *repo.Users, sessions *repo.Sessions, logins *repo.Logins,
	log *slog.Logger, ttl time.Duration, secure bool) *Service {

	return &Service{users: users, sessions: sessions, logins: logins,
		log: log, ttl: ttl, secure: secure}
}

// Login проверяет учётные данные и заводит сессию, выставляя cookie.
// login — почта: по нику больше не пускают. У кого почты нет, тот
// дописывает её себе на странице регистрации, см. web.registerSubmit.
//
// Запрос нужен целиком: откуда пришёл вход и чем, попадает в журнал,
// а адрес — ещё и в саму сессию.
func (s *Service) Login(ctx context.Context, w http.ResponseWriter, r *http.Request,
	login, password string) (*domain.User, error) {

	place, err := domain.ParseLoginPlace(r.RemoteAddr)
	if err != nil {
		// Записывать вход без адреса незачем: журнал ровно про то, откуда
		// входили. На живом сервере такого не бывает — RemoteAddr у tcp
		// всегда «адрес:порт», — но узнать, если случится, мы хотим.
		s.log.Warn("адрес входа не разобран, записи в журнале не будет",
			"remote", r.RemoteAddr, "err", err)
	}
	agent := r.UserAgent()

	user, err := s.users.ByEmail(ctx, login)
	if errors.Is(err, domain.ErrNotFound) {
		// Считаем хеш и на несуществующем логине, чтобы время ответа не
		// выдавало, зарегистрирован такой пользователь или нет.
		_ = VerifyPassword(password, dummyHash)
		s.record(ctx, nil, login, place, agent, false)
		return nil, domain.ErrInvalidLogin
	}
	if err != nil {
		return nil, err
	}
	// Отказ по паролю и отказ заблокированному пишем на самого владельца
	// почты: снаружи это одна и та же ошибка, а в журнале разница видна
	// по тому, есть ли рядом удачные входы.
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		s.record(ctx, &user.ID, login, place, agent, false)
		return nil, domain.ErrInvalidLogin
	}
	if !user.IsActive {
		s.record(ctx, &user.ID, login, place, agent, false)
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
	if err := s.sessions.Create(ctx, sum[:], user.ID, csrf, place.IP, expires); err != nil {
		return nil, err
	}
	s.setCookie(w, token, expires)
	s.record(ctx, &user.ID, login, place, agent, true)
	return user, nil
}

// record пишет попытку входа в журнал. Ошибка записи вход не отменяет:
// журнал важен, но человеку, который правильно ввёл пароль, до наших
// журналов дела нет. Пароль в запись не попадает ни в каком виде.
func (s *Service) record(ctx context.Context, userID *int64, login string,
	place domain.LoginPlace, agent string, ok bool) {

	if s.logins == nil || place.IP == "" {
		return
	}
	err := s.logins.Log(ctx, repo.LoginEvent{
		UserID: userID, Login: login, IP: place.IP, Subnet: place.Subnet,
		UserAgent: agent, OK: ok,
	})
	if err != nil {
		s.log.Error("не записан вход в журнал", "err", err, "ip", place.IP, "ok", ok)
	}
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
