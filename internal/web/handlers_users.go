package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"Vendetta_admin/internal/auth"
	"Vendetta_admin/internal/domain"
)

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	s.renderUsers(w, r, http.StatusOK, nil)
}

func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	users, err := s.users.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Значения по умолчанию обязательны: отсутствующий ключ карты
	// печатается в шаблоне как "<no value>".
	data := map[string]any{
		"Users": users, "Error": "",
		"FormEmail": "", "FormNickname": "", "FormRole": "user",
	}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "users", data)
}

// hx — пришёл ли запрос от htmx. Без него обработчики отвечают как раньше,
// переходом на /users: подменять строку в странице некому.
func hx(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

// userRow отдаёт одну строку таблицы доступов заново — уже с тем, что легло
// в базу. Пользователь перечитывается, а не берётся из того, что было до
// изменения: строка должна показывать состояние, а не намерение.
//
// field говорит, около чего показать сообщение: у поля проверок или в конце
// строки. Пустой field — обычная строка без сообщений.
func (s *Server) userRow(w http.ResponseWriter, r *http.Request, id int64, status int,
	field string, said map[string]any) {

	u, err := s.users.ByID(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"User": u, "Root": currentUser(r).IsRoot(), "Me": currentUser(r).ID,
		"CSRFToken": csrfToken(r), "Field": field,
	}
	for k, v := range said {
		data[k] = v
	}
	s.renderPartialStatus(w, r, status, "users", "user-row", data)
}

// rowDone — ответ на удавшееся изменение: свежая строка и слово о том,
// что оно применилось. Без слова htmx-ответ читался бы как «ничего
// не произошло»: половина настроек меняет строку незаметно.
func (s *Server) rowDone(w http.ResponseWriter, r *http.Request, id int64, field string) {
	s.userRow(w, r, id, http.StatusOK, field,
		map[string]any{"Note": langOf(r).T("users.saved")})
}

func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	email := strings.TrimSpace(r.PostFormValue("email"))
	nickname := strings.TrimSpace(r.PostFormValue("nickname"))
	password := r.PostFormValue("password")
	role := domain.Role(r.PostFormValue("role"))

	fail := func(msg string) {
		s.renderUsers(w, r, http.StatusUnprocessableEntity, map[string]any{
			"Error": msg, "FormEmail": email, "FormNickname": nickname, "FormRole": string(role),
		})
	}

	if err := domain.ValidateNickname(nickname); err != nil {
		fail(errText(r, err))
		return
	}
	// Почта необязательна: входить можно по нику. Но если её указали,
	// адрес должен быть разбираемым — по нему тоже пускают в систему.
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			fail(langOf(r).T("err.email.bad"))
			return
		}
	}
	// Рута назначить нельзя: он один и заводится при первом запуске.
	if role != domain.RoleUser && role != domain.RoleAdmin {
		fail(langOf(r).T("err.role.bad"))
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		fail(errText(r, err))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	created, err := s.users.Create(r.Context(), email, nickname, hash, role, &actor.ID)
	if errors.Is(err, domain.ErrEmailTaken) {
		fail(langOf(r).T("err.user.email.taken"))
		return
	}
	if errors.Is(err, domain.ErrNickTaken) {
		fail(langOf(r).T("err.user.nick.taken"))
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAudit(r, "user.create", created.ID,
		map[string]any{"nickname": nickname, "email": email, "role": string(role)})
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (s *Server) usersSetRole(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	role := domain.Role(r.PostFormValue("role"))
	if role != domain.RoleUser && role != domain.RoleAdmin {
		http.Error(w, "Недопустимая роль", http.StatusBadRequest)
		return
	}
	if err := s.users.SetRole(r.Context(), target.ID, role); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Смена роли обнуляет активные сессии: права должны примениться сразу.
	if err := s.sessions.DeleteByUser(r.Context(), target.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAudit(r, "user.set_role", target.ID,
		map[string]any{"user": target.Display(), "from": string(target.Role), "to": string(role)})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.rowDone(w, r, target.ID, "role")
}

func (s *Server) usersSetActive(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	active := r.PostFormValue("active") == "true"
	if err := s.users.SetActive(r.Context(), target.ID, active); err != nil {
		s.serverError(w, r, err)
		return
	}
	if !active {
		if err := s.sessions.DeleteByUser(r.Context(), target.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.logAudit(r, "user.set_active", target.ID, map[string]any{"user": target.Display(), "active": active})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.rowDone(w, r, target.ID, "active")
}

// usersSetGamesAccess выдаёт и снимает доступ к разделу «Игры». Роут стоит
// под рутом, а не под админом: раздел работает с общим игровым аккаунтом
// проекта, и передавать право раздачи дальше по лестнице ролей мы не хотим.
func (s *Server) usersSetGamesAccess(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	allowed := r.PostFormValue("access") == "true"
	if err := s.users.SetGamesAccess(r.Context(), target.ID, allowed); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Сессии не сбрасываем: пользователь читается из базы на каждый запрос,
	// так что снятый доступ действует со следующей же страницы.
	s.logAudit(r, "user.set_games_access", target.ID,
		map[string]any{"user": target.Display(), "access": allowed})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.rowDone(w, r, target.ID, "games")
}

// usersSetPlan меняет пакет доступа. Право рутовое, как и всё в этом ряду:
// пакет решает, сколько человеку можно, и раздавать это по лестнице ролей
// мы не хотим.
//
// Записи сверх нового предела не трогаем. Человек их собирал, комментарии
// писал сам, и удалять чужой труд из-за смены пакета нечестно: добавлять
// он просто не сможет, пока не разгребёт сам.
func (s *Server) usersSetPlan(w http.ResponseWriter, r *http.Request) {
	// Не manageableTarget: тот запрещает трогать себя и рута, и правильно
	// делает — роль, блокировка и удаление себя закрыли бы вход насовсем.
	// С пакетом иначе: рут ставит его и себе, чтобы посмотреть на админку
	// глазами обычного человека, а обратно вернёт когда угодно — раздел
	// открыт ему по роли, а не по пакету. Роут и так рутовый.
	target, ok := s.userTarget(w, r)
	if !ok {
		return
	}
	plan := domain.Plan(r.PostFormValue("plan"))
	if !plan.Valid() {
		http.Error(w, "Недопустимый пакет", http.StatusBadRequest)
		return
	}
	if err := s.users.SetPlan(r.Context(), target.ID, plan); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Сессии не сбрасываем: пользователь читается из базы на каждый запрос,
	// так что новый пакет действует со следующей же страницы.
	s.logAudit(r, "user.set_plan", target.ID,
		map[string]any{"user": target.Display(), "from": string(target.Plan), "to": string(plan)})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.rowDone(w, r, target.ID, "plan")
}

// usersSetMapChecks меняет дневное число проверок карты. Право рутовое
// по той же причине, что и доступ к разделу: каждая проверка — это данные
// с игрового сервера, добытые общим аккаунтом проекта.
func (s *Server) usersSetMapChecks(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	typed := strings.TrimSpace(r.PostFormValue("checks"))
	checks, err := strconv.Atoi(typed)
	// Ноль — это запрет смотреть карты, и он осмысленный. А вот отрицательное
	// число и мусор в поле означают опечатку, а не намерение.
	if err != nil || checks < 0 || checks > maxMapChecks {
		msg := langOf(r).T("err.checks.bad", maxMapChecks)
		if !hx(r) {
			s.renderUsers(w, r, http.StatusUnprocessableEntity, map[string]any{"Error": msg})
			return
		}
		// Набранное возвращаем в поле: молча подменить его сохранённым
		// значило бы спрятать опечатку, а не показать её.
		s.userRow(w, r, target.ID, http.StatusUnprocessableEntity, "checks",
			map[string]any{"Error": msg, "ChecksInput": typed})
		return
	}
	if err := s.users.SetMapChecks(r.Context(), target.ID, checks); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Уже потраченное за сегодня не возвращаем и не отбираем: счёт идёт
	// по дню, а новое число действует с этого мгновения.
	s.logAudit(r, "user.set_map_checks", target.ID,
		map[string]any{"user": target.Display(), "checks": checks})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.rowDone(w, r, target.ID, "checks")
}

// maxMapChecks — потолок для поля: не запрет, а защита от лишнего нуля
// в конце. Столько раз в сутки карту всё равно никто не смотрит.
const maxMapChecks = 500

func (s *Server) usersResetPassword(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	password := r.PostFormValue("password")
	if err := auth.ValidatePassword(password); err != nil {
		if !hx(r) {
			s.renderUsers(w, r, http.StatusUnprocessableEntity,
				map[string]any{"Error": errText(r, err)})
			return
		}
		s.userRow(w, r, target.ID, http.StatusUnprocessableEntity, "password",
			map[string]any{"Error": errText(r, err)})
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.users.SetPassword(r.Context(), target.ID, hash); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.sessions.DeleteByUser(r.Context(), target.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAudit(r, "user.reset_password", target.ID, map[string]any{"user": target.Display()})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	// Своё слово: «сохранено» о пароле звучало бы как о настройке, а сброс
	// пароля ещё и выкидывает человека из всех сессий.
	s.userRow(w, r, target.ID, http.StatusOK, "password",
		map[string]any{"Note": langOf(r).T("users.password.done")})
}

func (s *Server) usersDelete(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	if err := s.users.Delete(r.Context(), target.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAudit(r, "user.delete", target.ID,
		map[string]any{"user": target.Display(), "role": string(target.Role)})
	if !hx(r) {
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	// Пустой ответ на месте строки — она и пропадает. Показывать нечего:
	// пользователя больше нет.
	w.WriteHeader(http.StatusOK)
}

// manageableTarget разбирает id из пути и проверяет право актора им управлять.
// userTarget — пользователь из адреса, без вопроса о праве его менять.
// Право проверяет тот, кто вызывает: у большинства правок оно одно
// (CanManage), а у пакета своё.
func (s *Server) userTarget(w http.ResponseWriter, r *http.Request) (*domain.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Некорректный id", http.StatusBadRequest)
		return nil, false
	}
	target, err := s.users.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "Пользователь не найден", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return target, true
}

func (s *Server) manageableTarget(w http.ResponseWriter, r *http.Request) (*domain.User, bool) {
	target, ok := s.userTarget(w, r)
	if !ok {
		return nil, false
	}
	if !domain.CanManage(currentUser(r), target) {
		http.Error(w, "Недостаточно прав для этого действия", http.StatusForbidden)
		return nil, false
	}
	return target, true
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	entries, err := s.audit.Recent(r.Context(), 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "audit", map[string]any{"Entries": entries})
}

// logAudit не прерывает операцию: журнал важен, но не критичен для ответа.
func (s *Server) logAudit(r *http.Request, action string, targetID int64, payload map[string]any) {
	s.logAuditOn(r, action, "user", targetID, payload)
}

func (s *Server) logAuditOn(r *http.Request, action, targetType string, targetID int64, payload map[string]any) {
	if err := s.audit.Log(r.Context(), currentUser(r), action, targetType, targetID, payload); err != nil {
		s.log.Error("не записан журнал", "err", err, "action", action, "target_id", targetID)
	}
}
