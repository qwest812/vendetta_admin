package repo

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

type Audit struct{ pool *pgxpool.Pool }

func NewAudit(pool *pgxpool.Pool) *Audit { return &Audit{pool: pool} }

type AuditEntry struct {
	ID         int64
	ActorEmail string
	Action     string
	TargetType string
	TargetID   string
	Payload    map[string]any
	CreatedAt  time.Time
}

// Log пишет запись журнала. Ошибку логируем, но не роняем операцию:
// журнал не должен блокировать основную работу.
func (r *Audit) Log(ctx context.Context, actor *domain.User, action, targetType string, targetID int64, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO audit_log (actor_id, actor_email, action, target_type, target_id, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		actor.ID, actor.Display(), action, targetType, strconv.FormatInt(targetID, 10), raw)
	return err
}

func (r *Audit) Recent(ctx context.Context, limit int) ([]*AuditEntry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, actor_email, action, target_type, target_id, payload, created_at
		 FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var raw []byte
		if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
