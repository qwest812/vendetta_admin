package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

// feedbackList — список обращений. Своё видит каждый, все — админ; фильтр по
// статусу нужен только тому, у кого список общий.
func (s *Server) feedbackList(w http.ResponseWriter, r *http.Request) {
	s.renderFeedback(w, r, http.StatusOK, "", "", "")
}

func (s *Server) renderFeedback(w http.ResponseWriter, r *http.Request, code int, errMsg, subject, body string) {
	me := currentUser(r)
	status, _ := domain.ParseTicketStatus(r.URL.Query().Get("status"))

	// Чужие обращения показываются только админу, поэтому обычному
	// пользователю список всегда сужается по автору — из сессии, а не из адреса.
	var author *int64
	if !me.IsAdmin() {
		author = &me.ID
		status = ""
	}

	tickets, err := s.feedback.List(r.Context(), author, status)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.render(w, r, code, "feedback", map[string]any{
		"Tickets": tickets, "Status": string(status), "Error": errMsg,
		// Что человек успел написать: после ошибки форма не должна
		// заставлять набирать всё заново.
		"Subject": subject, "Body": body,
	})
}

func (s *Server) feedbackCreate(w http.ResponseWriter, r *http.Request) {
	subject := strings.TrimSpace(r.PostFormValue("subject"))
	body := strings.TrimSpace(r.PostFormValue("body"))

	if msg, ok := checkTicketText(r, subject, body); !ok {
		s.renderFeedback(w, r, http.StatusUnprocessableEntity, msg, subject, body)
		return
	}

	id, err := s.feedback.Create(r.Context(), currentUser(r), subject, body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/feedback/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// feedbackTicket — переписка по одному обращению.
func (s *Server) feedbackTicket(w http.ResponseWriter, r *http.Request) {
	ticket, ok := s.loadTicket(w, r)
	if !ok {
		return
	}
	s.renderTicket(w, r, http.StatusOK, ticket, "")
}

func (s *Server) renderTicket(w http.ResponseWriter, r *http.Request, code int,
	ticket domain.Ticket, errMsg string) {

	messages, err := s.feedback.Messages(r.Context(), ticket.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Автор открыл переписку — значит, прочитал: гасим «есть ответ». Отметка
	// ставится после чтения сообщений, чтобы пометка на этой же странице
	// ещё была видна.
	me := currentUser(r)
	if ticket.IsAuthor(me) {
		if err := s.feedback.MarkSeen(r.Context(), ticket.ID, me.ID); err != nil {
			s.log.Error("не отмечен просмотр обращения", "err", err, "ticket_id", ticket.ID)
		}
	}

	s.render(w, r, code, "feedback_ticket", map[string]any{
		"Ticket": ticket, "Messages": messages, "Error": errMsg,
	})
}

// feedbackReply — ответ в переписку. Пишут обе стороны: и админ, и автор.
func (s *Server) feedbackReply(w http.ResponseWriter, r *http.Request) {
	ticket, ok := s.loadTicket(w, r)
	if !ok {
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if !validTicketBody(body) {
		s.renderTicket(w, r, http.StatusUnprocessableEntity, ticket, langOf(r).T("err.feedback.body"))
		return
	}

	me := currentUser(r)
	err := s.feedback.AddMessage(r.Context(), ticket.ID, me, body, ticket.StatusAfterMessage(me))
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, langOf(r).T("err.feedback.notfound"), http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/feedback/"+strconv.FormatInt(ticket.ID, 10), http.StatusSeeOther)
}

// feedbackClose — «вопрос снят». Закрывает админ или сам автор; чужое
// обращение постороннему недоступно так же, как и на просмотр.
func (s *Server) feedbackClose(w http.ResponseWriter, r *http.Request) {
	ticket, ok := s.loadTicket(w, r)
	if !ok {
		return
	}
	if !ticket.CanClose(currentUser(r)) {
		http.Error(w, langOf(r).T("err.forbidden"), http.StatusForbidden)
		return
	}
	if err := s.feedback.Close(r.Context(), ticket.ID, currentUser(r)); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logAuditOn(r, "feedback.close", "ticket", ticket.ID,
		map[string]any{"subject": ticket.Subject, "author": ticket.AuthorName})
	http.Redirect(w, r, "/feedback/"+strconv.FormatInt(ticket.ID, 10), http.StatusSeeOther)
}

// loadTicket достаёт обращение и сразу решает, показывать ли его. Чужое
// обращение отвечает «не найдено», а не «нельзя»: постороннему незачем знать
// даже о том, что оно есть.
func (s *Server) loadTicket(w http.ResponseWriter, r *http.Request) (domain.Ticket, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, langOf(r).T("err.badid"), http.StatusBadRequest)
		return domain.Ticket{}, false
	}
	ticket, err := s.feedback.ByID(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, langOf(r).T("err.feedback.notfound"), http.StatusNotFound)
		return domain.Ticket{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return domain.Ticket{}, false
	}
	if !ticket.CanView(currentUser(r)) {
		http.Error(w, langOf(r).T("err.feedback.notfound"), http.StatusNotFound)
		return domain.Ticket{}, false
	}
	return ticket, true
}

func checkTicketText(r *http.Request, subject, body string) (string, bool) {
	n := len([]rune(subject))
	if n < domain.TicketSubjectMin || n > domain.TicketSubjectMax {
		return langOf(r).T("err.feedback.subject"), false
	}
	if !validTicketBody(body) {
		return langOf(r).T("err.feedback.body"), false
	}
	return "", true
}

func validTicketBody(body string) bool {
	n := len([]rune(body))
	return n >= domain.TicketBodyMin && n <= domain.TicketBodyMax
}
