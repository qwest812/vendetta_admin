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
		// Подсети входов — рутовая колонка; обычному админу приезжает
		// пустая карта, и строка показывает прочерк.
		"Subnets": s.subnetsByUser(r),
	}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "users", data)
}

// hx — пришёл ли запрос от htmx. Без него обработчики отвечают переходом:
// подменять кусок страницы некому.
func hx(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

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
	// Почта обязательна: входят только по ней. Адрес — только сам адрес,
	// «Имя <a@b>» разбирается, но логином служить не может.
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		fail(langOf(r).T("err.email.bad"))
		return
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
	accountDone(w, r, target.ID, "")
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
	accountDone(w, r, target.ID, "")
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
	accountDone(w, r, target.ID, "")
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
	accountDone(w, r, target.ID, "")
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
		// Набранное возвращаем в поле: молча подменить его сохранённым
		// значило бы спрятать опечатку, а не показать её.
		s.renderAccount(w, r, http.StatusUnprocessableEntity, target, accountView{
			Error: langOf(r).T("err.checks.bad", maxMapChecks), ChecksInput: typed})
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
	accountDone(w, r, target.ID, "")
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
		s.renderAccount(w, r, http.StatusUnprocessableEntity, target, accountView{Error: errText(r, err)})
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
	// Своё слово: «сохранено» о пароле звучало бы как о настройке, а сброс
	// пароля ещё и выкидывает человека из всех сессий.
	accountDone(w, r, target.ID, "password")
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
	// Страницы аккаунта больше нет — возвращаемся к списку.
	http.Redirect(w, r, "/users", http.StatusSeeOther)
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
	s.logAuditAs(r, currentUser(r), action, targetType, targetID, payload)
}

// logAuditBy — запись от имени того, кто ещё не вошёл: регистрация идёт
// без сессии, и действующее лицо в ней — сам регистрирующийся.
func (s *Server) logAuditBy(r *http.Request, actor *domain.User, action string, targetID int64, payload map[string]any) {
	s.logAuditAs(r, actor, action, "user", targetID, payload)
}

func (s *Server) logAuditAs(r *http.Request, actor *domain.User, action, targetType string, targetID int64, payload map[string]any) {
	if err := s.audit.Log(r.Context(), actor, action, targetType, targetID, payload); err != nil {
		s.log.Error("не записан журнал", "err", err, "action", action, "target_id", targetID)
	}
}
