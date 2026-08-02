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
	data := map[string]any{"Users": users, "Error": "", "FormEmail": "", "FormRole": "user"}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "users", data)
}

func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	role := domain.Role(r.PostFormValue("role"))

	fail := func(msg string) {
		s.renderUsers(w, r, http.StatusUnprocessableEntity, map[string]any{
			"Error": msg, "FormEmail": email, "FormRole": string(role),
		})
	}

	if _, err := mail.ParseAddress(email); err != nil {
		fail("Некорректный адрес почты")
		return
	}
	// Рута назначить нельзя: он один и заводится при первом запуске.
	if role != domain.RoleUser && role != domain.RoleAdmin {
		fail("Можно выдать только роль «пользователь» или «администратор»")
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		fail(err.Error())
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	created, err := s.users.Create(r.Context(), email, hash, role, &actor.ID)
	if errors.Is(err, domain.ErrEmailTaken) {
		fail("Пользователь с такой почтой уже есть")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.logAudit(r, "user.create", created.ID, map[string]any{"email": email, "role": string(role)})
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
		map[string]any{"email": target.Email, "from": string(target.Role), "to": string(role)})
	http.Redirect(w, r, "/users", http.StatusSeeOther)
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
	s.logAudit(r, "user.set_active", target.ID, map[string]any{"email": target.Email, "active": active})
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (s *Server) usersResetPassword(w http.ResponseWriter, r *http.Request) {
	target, ok := s.manageableTarget(w, r)
	if !ok {
		return
	}
	password := r.PostFormValue("password")
	if err := auth.ValidatePassword(password); err != nil {
		s.renderUsers(w, r, http.StatusUnprocessableEntity, map[string]any{"Error": err.Error()})
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
	s.logAudit(r, "user.reset_password", target.ID, map[string]any{"email": target.Email})
	http.Redirect(w, r, "/users", http.StatusSeeOther)
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
		map[string]any{"email": target.Email, "role": string(target.Role)})
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// manageableTarget разбирает id из пути и проверяет право актора им управлять.
func (s *Server) manageableTarget(w http.ResponseWriter, r *http.Request) (*domain.User, bool) {
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
