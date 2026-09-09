package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// feedbackLimit — сколько обращений показывает список. Разговоров тут не
// тысячи, а страница должна оставаться одной страницей.
const feedbackLimit = 200

// Feedback — обращения и переписка по ним.
type Feedback struct{ pool *pgxpool.Pool }

func NewFeedback(pool *pgxpool.Pool) *Feedback { return &Feedback{pool: pool} }

// ticketColumns: счётчик сообщений и «есть ответ» считаются тем же запросом —
// иначе список из двадцати обращений превратился бы в сорок запросов.
// «Есть ответ» — это чужое сообщение новее отметки о том, когда автор
// последний раз открывал переписку.
const ticketColumns = `
	t.id, t.author_id, t.author_name, t.subject, t.status,
	t.created_at, t.updated_at, t.closed_at,
	(SELECT count(*) FROM feedback_messages m WHERE m.ticket_id = t.id),
	EXISTS (SELECT 1 FROM feedback_messages m
	         WHERE m.ticket_id = t.id
	           AND m.created_at > t.author_seen_at
	           AND m.author_id IS DISTINCT FROM t.author_id)`

func scanTicket(row pgx.Row) (domain.Ticket, error) {
	var t domain.Ticket
	err := row.Scan(&t.ID, &t.AuthorID, &t.AuthorName, &t.Subject, &t.Status,
		&t.CreatedAt, &t.UpdatedAt, &t.ClosedAt, &t.Messages, &t.HasReply)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	return t, err
}

// List отдаёт обращения свежим разговором вверх. Пустой authorID означает
// «все» — так список видит админ; обычный пользователь получает только свои.
// Пустой status — без фильтра.
func (r *Feedback) List(ctx context.Context, authorID *int64, status domain.TicketStatus) ([]domain.Ticket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+ticketColumns+`
		FROM feedback_tickets t
		WHERE ($1::bigint IS NULL OR t.author_id = $1)
		  AND ($2 = '' OR t.status = $2)
		ORDER BY t.updated_at DESC, t.id DESC
		LIMIT $3`, authorID, string(status), feedbackLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Feedback) ByID(ctx context.Context, id int64) (domain.Ticket, error) {
	return scanTicket(r.pool.QueryRow(ctx,
		`SELECT `+ticketColumns+` FROM feedback_tickets t WHERE t.id = $1`, id))
}

// Messages — переписка по обращению, старое сверху: разговор читают сверху вниз.
func (r *Feedback) Messages(ctx context.Context, ticketID int64) ([]domain.TicketMessage, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, ticket_id, author_id, author_name, from_staff, body, created_at
		 FROM feedback_messages WHERE ticket_id = $1 ORDER BY created_at, id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.TicketMessage
	for rows.Next() {
		var m domain.TicketMessage
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.AuthorName,
			&m.FromStaff, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Create заводит обращение вместе с первым сообщением: обращения без текста
// не бывает, поэтому и пишутся они одной транзакцией.
func (r *Feedback) Create(ctx context.Context, author *domain.User, subject, body string) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO feedback_tickets (author_id, author_name, subject) VALUES ($1, $2, $3) RETURNING id`,
		author.ID, author.Display(), subject).Scan(&id); err != nil {
		return 0, err
	}
	if err := insertMessage(ctx, tx, id, author, body); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// AddMessage дописывает сообщение и переставляет статус: закрытое обращение
// поднимает обратно новое слово автора, ответ админа — нет. Решает это
// предметная область, здесь только записывается.
func (r *Feedback) AddMessage(ctx context.Context, ticketID int64, author *domain.User,
	body string, status domain.TicketStatus) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertMessage(ctx, tx, ticketID, author, body); err != nil {
		return err
	}
	// Автор, который только что написал, ничего непрочитанного не оставил —
	// поэтому отметка о просмотре двигается вместе с его сообщением.
	tag, err := tx.Exec(ctx, `
		UPDATE feedback_tickets
		   SET updated_at = now(),
		       status = $2,
		       closed_at = CASE WHEN $2 = 'open' THEN NULL ELSE closed_at END,
		       author_seen_at = CASE WHEN author_id = $3 THEN now() ELSE author_seen_at END
		 WHERE id = $1`, ticketID, string(status), author.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit(ctx)
}

func insertMessage(ctx context.Context, tx pgx.Tx, ticketID int64, author *domain.User, body string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO feedback_messages (ticket_id, author_id, author_name, from_staff, body)
		 VALUES ($1, $2, $3, $4, $5)`,
		ticketID, author.ID, author.Display(), author.IsAdmin(), body)
	return err
}

// Close закрывает обращение. Уже закрытое закрывается вхолостую: кнопку могли
// нажать дважды, и ошибка тут сказала бы не о том.
func (r *Feedback) Close(ctx context.Context, id int64, by *domain.User) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE feedback_tickets
		   SET status = 'closed', closed_at = now(), closed_by = $2, updated_at = now()
		 WHERE id = $1 AND status = 'open'`, id, by.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Ничего не поменяли: либо обращения нет, либо оно уже закрыто.
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM feedback_tickets WHERE id = $1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return domain.ErrNotFound
		}
	}
	return nil
}

// MarkSeen отмечает, что автор открыл переписку: после этого «есть ответ»
// гаснет. Чужой просмотр отметку не двигает — она про автора.
func (r *Feedback) MarkSeen(ctx context.Context, ticketID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE feedback_tickets SET author_seen_at = now() WHERE id = $1 AND author_id = $2`,
		ticketID, userID)
	return err
}

// Badge — число рядом с разделом в шапке. Админу оно означает «столько
// обращений ждут ответа», автору — «столько ваших обращений отвечены».
func (r *Feedback) Badge(ctx context.Context, u *domain.User) (int, error) {
	var n int
	if u.IsAdmin() {
		err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM feedback_tickets WHERE status = 'open'`).Scan(&n)
		return n, err
	}
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM feedback_tickets t
		 WHERE t.author_id = $1
		   AND EXISTS (SELECT 1 FROM feedback_messages m
		                WHERE m.ticket_id = t.id
		                  AND m.created_at > t.author_seen_at
		                  AND m.author_id IS DISTINCT FROM t.author_id)`, u.ID).Scan(&n)
	return n, err
}
