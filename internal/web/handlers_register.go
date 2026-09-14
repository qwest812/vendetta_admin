package web

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"Vendetta_admin/internal/auth"
	"Vendetta_admin/internal/domain"
)

// nickSource — у кого спросить ник по игровому ID. Это сайт игры, но
// регистрации от него нужен один вопрос, а не весь раздел «Игры».
type nickSource interface {
	Username(ctx context.Context, siteUserID string) (string, error)
}

// nickTimeout — сколько ждать ник от сайта игры. Человек стоит перед
// формой, и минуту, как партии, он ждать не будет.
const nickTimeout = 20 * time.Second

// honeypotField — поле, которого человек не видит. Боты заполняют всё,
// что нашли в форме, и выдают себя.
const honeypotField = "website"

func (s *Server) registerForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderRegister(w, r, http.StatusOK, "", "", "")
}

// registerSubmit — регистрация без участия админа: почта, игровой ID
// и пароль. Ник не спрашиваем, а берём у игры по ID.
//
// Если аккаунт с этим ID (или, у кого ID не записан, с этим ником) уже
// заведён админом без почты, новый не заводится: почта дописывается к
// старому. Но только тому, кто знает его нынешний пароль — игровые ID
// видны в любой партии, и без пароля так забирали бы чужие аккаунты.
func (s *Server) registerSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, langOf(r).T("err.badform"), http.StatusBadRequest)
		return
	}
	if r.PostFormValue(honeypotField) != "" {
		s.log.Warn("регистрация: заполнено скрытое поле", "ip", r.RemoteAddr)
		http.Error(w, langOf(r).T("err.badform"), http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.PostFormValue("email"))
	gameID := strings.TrimSpace(r.PostFormValue("game_id"))
	password := r.PostFormValue("password")
	fail := func(status int, msg string) {
		s.renderRegister(w, r, status, msg, email, gameID)
	}

	// Адрес — только сам адрес: «Имя <a@b>» разбирается, но входить
	// с таким логином никто не станет.
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		fail(http.StatusUnprocessableEntity, langOf(r).T("err.email.bad"))
		return
	}
	if err := domain.ValidateSignupGameID(gameID); err != nil {
		fail(http.StatusUnprocessableEntity, errText(r, err))
		return
	}
	// Пароль проверяется сразу, хоть заведённому аккаунту и нужен нынешний:
	// правило одно с «Доступами», и короче 12 символов паролей в базе нет.
	if err := auth.ValidatePassword(password); err != nil {
		fail(http.StatusUnprocessableEntity, errText(r, err))
		return
	}

	// Предел — после проверки формы: опечатка в адресе ни игру, ни базу
	// не спрашивает, и засчитывать её незачем.
	place, err := domain.ParseLoginPlace(r.RemoteAddr)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !s.signups.allow(place.Subnet, time.Now()) {
		s.log.Warn("регистрация: предел попыток с подсети", "subnet", place.Subnet)
		fail(http.StatusTooManyRequests, langOf(r).T("register.toomany"))
		return
	}

	existing, err := s.users.ByGameID(r.Context(), gameID)
	if err == nil {
		s.claimAccount(w, r, existing, email, gameID, password, fail)
		return
	}
	if !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}

	if s.nicks == nil {
		fail(http.StatusServiceUnavailable, langOf(r).T("register.unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nickTimeout)
	nickname, err := s.nicks.Username(ctx, gameID)
	cancel()
	if err != nil {
		s.log.Error("регистрация: ник у игры не получен", "game_id", gameID, "err", err)
		fail(http.StatusBadGateway, langOf(r).T("register.gamefail"))
		return
	}
	if nickname == "" {
		fail(http.StatusUnprocessableEntity, langOf(r).T("register.unknownid"))
		return
	}

	// Аккаунт, заведённый до того, как ID стали записывать, узнаётся
	// по нику. Если же у тёзки ID свой — это другой человек.
	existing, err = s.users.ByNickname(r.Context(), nickname)
	if err == nil {
		if existing.GameID != "" {
			fail(http.StatusConflict, langOf(r).T("register.nicktaken", nickname))
			return
		}
		s.claimAccount(w, r, existing, email, gameID, password, fail)
		return
	}
	if !errors.Is(err, domain.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	created, err := s.users.Register(r.Context(), email, nickname, gameID, hash)
	if errors.Is(err, domain.ErrEmailTaken) {
		fail(http.StatusConflict, langOf(r).T("register.emailtaken"))
		return
	}
	// Ник и ID заняты быть не могут — их только что проверили. Сюда
	// попадают две регистрации наперегонки, и вторая узнаёт, что опоздала.
	if errors.Is(err, domain.ErrNickTaken) {
		fail(http.StatusConflict, langOf(r).T("register.nicktaken", nickname))
		return
	}
	if errors.Is(err, domain.ErrGameIDTaken) {
		fail(http.StatusConflict, langOf(r).T("register.exists"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAuditBy(r, created, "user.register", created.ID,
		map[string]any{"email": email, "nickname": nickname, "game_id": gameID})
	s.log.Info("зарегистрирован пользователь", "user_id", created.ID, "nickname", nickname)
	s.enter(w, r, email, password)
}

// claimAccount дописывает почту аккаунту, который уже заведён. Пароль
// спрашивается нынешний: он и есть доказательство, что аккаунт свой.
func (s *Server) claimAccount(w http.ResponseWriter, r *http.Request, existing *domain.User,
	email, gameID, password string, fail func(int, string)) {

	if existing.Email != "" {
		fail(http.StatusConflict, langOf(r).T("register.exists"))
		return
	}
	if err := auth.VerifyPassword(password, existing.PasswordHash); err != nil {
		s.log.Warn("регистрация: не тот пароль к заведённому аккаунту",
			"user_id", existing.ID, "ip", r.RemoteAddr)
		fail(http.StatusUnauthorized, langOf(r).T("register.claim.password"))
		return
	}
	if !existing.IsActive {
		fail(http.StatusForbidden, langOf(r).T("register.disabled"))
		return
	}

	err := s.users.AttachEmail(r.Context(), existing.ID, email, gameID)
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		fail(http.StatusConflict, langOf(r).T("register.emailtaken"))
		return
	// Почту успели дописать между проверкой и записью, или названный ID
	// уже записан за другим аккаунтом.
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrGameIDTaken):
		fail(http.StatusConflict, langOf(r).T("register.exists"))
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}

	s.logAuditBy(r, existing, "user.claim", existing.ID,
		map[string]any{"email": email, "game_id": gameID})
	s.log.Info("к аккаунту добавлена почта", "user_id", existing.ID, "nickname", existing.Nickname)
	s.enter(w, r, email, password)
}

// enter заводит сессию сразу после регистрации тем же путём, что и обычный
// вход: так он попадает в журнал входов наравне с остальными.
func (s *Server) enter(w http.ResponseWriter, r *http.Request, email, password string) {
	if _, err := s.auth.Login(r.Context(), w, r, email, password); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderRegister(w http.ResponseWriter, r *http.Request, status int,
	errMsg, email, gameID string) {

	s.render(w, r, status, "register", map[string]any{
		"Error": errMsg, "Email": email, "GameID": gameID,
	})
}
