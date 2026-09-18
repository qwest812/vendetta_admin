package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
)

// editLogins — сколько последних входов показывать на странице аккаунта.
// Полный журнал по человеку — по ссылке в «Входы».
const editLogins = 10

// accountView — что сказать на странице аккаунта сверх самих данных.
// Form — набранное в форме данных: при отказе оно остаётся в полях.
// ChecksInput — набранное в поле проверок, по той же причине.
type accountView struct {
	Form        *repo.AccountFields
	Error       string
	Notice      string
	ChecksInput string
}

// userEditForm — страница аккаунта: все настройки человека в одном месте.
// Список в «Доступах» только перечисляет людей, а меняется всё здесь —
// в строку таблицы столько настроек не помещалось.
//
// Открыта админам, как и сам список, но каждый раздел показывается по
// своему праву: данные, пакет, «Игры», проверки и удаление — руту, роль,
// блокировка и пароль — тому, кто вправе управлять этим человеком.
func (s *Server) userEditForm(w http.ResponseWriter, r *http.Request) {
	target, ok := s.userTarget(w, r)
	if !ok {
		return
	}
	var v accountView
	switch r.URL.Query().Get("saved") {
	case "":
	case "password":
		v.Notice = langOf(r).T("users.password.done")
	default:
		v.Notice = langOf(r).T("users.saved")
	}
	s.renderAccount(w, r, http.StatusOK, target, v)
}

// accountDone — правка удалась: обратно на страницу аккаунта со словом
// о ней. Переходом, а не отрисовкой: обновление страницы не должно
// отправлять форму второй раз.
func accountDone(w http.ResponseWriter, r *http.Request, id int64, what string) {
	if what == "" {
		what = "1"
	}
	http.Redirect(w, r, editPath(id)+"?saved="+what, http.StatusSeeOther)
}

func (s *Server) userEditSave(w http.ResponseWriter, r *http.Request) {
	target, ok := s.userTarget(w, r)
	if !ok {
		return
	}
	f := repo.AccountFields{
		Nickname: strings.TrimSpace(r.PostFormValue("nickname")),
		Email:    strings.TrimSpace(r.PostFormValue("email")),
		GameID:   strings.TrimSpace(r.PostFormValue("game_id")),
		FullName: strings.TrimSpace(r.PostFormValue("full_name")),
		City:     strings.TrimSpace(r.PostFormValue("city")),
	}
	// Набранное возвращаем в форму: перенабирать из-за одной ошибки обидно.
	fail := func(status int, msg string) {
		s.renderAccount(w, r, status, target, accountView{Form: &f, Error: msg})
	}

	if err := domain.ValidateNickname(f.Nickname); err != nil {
		fail(http.StatusUnprocessableEntity, errText(r, err))
		return
	}
	// Почту можно и стереть — у старых аккаунтов её не было. Но без почты
	// не войти, и рут без неё запер бы админку сам от себя.
	if f.Email == "" && target.IsRoot() {
		fail(http.StatusUnprocessableEntity, langOf(r).T("err.email.bad"))
		return
	}
	if f.Email != "" {
		if addr, err := mail.ParseAddress(f.Email); err != nil || addr.Address != f.Email {
			fail(http.StatusUnprocessableEntity, langOf(r).T("err.email.bad"))
			return
		}
	}
	if err := domain.ValidateProfile(f.FullName, f.City, f.GameID); err != nil {
		fail(http.StatusUnprocessableEntity, errText(r, err))
		return
	}

	err := s.users.UpdateAccount(r.Context(), target.ID, f)
	switch {
	case errors.Is(err, domain.ErrEmailTaken):
		fail(http.StatusConflict, langOf(r).T("err.user.email.taken"))
		return
	case errors.Is(err, domain.ErrNickTaken):
		fail(http.StatusConflict, langOf(r).T("err.user.nick.taken"))
		return
	case errors.Is(err, domain.ErrGameIDTaken):
		fail(http.StatusConflict, errText(r, err))
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}

	// В журнал — только то, что поменялось, с прежним значением рядом:
	// по записи должно быть видно, чья почта была до правки.
	changes := map[string]any{"user": target.Display()}
	was := accountOf(target)
	for _, c := range []struct{ key, from, to string }{
		{"nickname", was.Nickname, f.Nickname},
		{"email", was.Email, f.Email},
		{"game_id", was.GameID, f.GameID},
		{"full_name", was.FullName, f.FullName},
		{"city", was.City, f.City},
	} {
		if c.from != c.to {
			// Одной строкой «было → стало»: журнал печатает значения как
			// есть, и вложенная карта читалась бы там как map[…].
			changes[c.key] = orDash(c.from) + " → " + orDash(c.to)
		}
	}
	if len(changes) > 1 {
		s.logAudit(r, "user.update", target.ID, changes)
	}
	accountDone(w, r, target.ID, "")
}

func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, status int,
	target *domain.User, v accountView) {

	me := currentUser(r)
	form := accountOf(target)
	if v.Form != nil {
		form = *v.Form
	}
	checks := strconv.Itoa(target.MapChecks)
	if v.ChecksInput != "" {
		checks = v.ChecksInput
	}

	// Входы и подсети — рутовые сведения, как и сама страница «Входы».
	var logins []repo.LoginEvent
	if me.IsRoot() && s.logins != nil {
		all, err := s.logins.Recent(r.Context(), repo.LoginFilter{UserID: &target.ID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		logins = all[:min(len(all), editLogins)]
	}
	s.render(w, r, status, "user_edit", map[string]any{
		"Account": target, "Form": form, "Error": v.Error, "Notice": v.Notice,
		"Checks": checks, "MaxChecks": maxMapChecks,
		"Logins": logins, "Subnets": s.subnetsByUser(r)[target.ID],
		"Root": me.IsRoot(),
		// Роль, блокировка, пароль, «Игры», проверки и удаление — тем же
		// правом, что и прежде в строке списка: не себя и не рута.
		"CanManage": domain.CanManage(me, target),
	})
}

func accountOf(u *domain.User) repo.AccountFields {
	return repo.AccountFields{
		Nickname: u.Nickname, Email: u.Email, GameID: u.GameID,
		FullName: u.FullName, City: u.City,
	}
}

func editPath(id int64) string { return "/users/" + strconv.FormatInt(id, 10) + "/edit" }

// orDash — пустое значение в журнале прочерком: «→ x» не отличить
// от обрезанной строки.
func orDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}
