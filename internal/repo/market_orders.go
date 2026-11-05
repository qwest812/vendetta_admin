package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Vendetta_admin/internal/domain"
)

// MarketOrders хранит заявки на покупку с биржи партий.
type MarketOrders struct{ pool *pgxpool.Pool }

func NewMarketOrders(pool *pgxpool.Pool) *MarketOrders { return &MarketOrders{pool: pool} }

const marketOrderColumns = `id, game_id, resource_id, resource, budget, max_price, every_min,
	spent, bought, next_at, run_at, result, added_at`

func scanMarketOrders(rows pgx.Rows) ([]domain.MarketOrder, error) {
	defer rows.Close()
	var out []domain.MarketOrder
	for rows.Next() {
		var (
			o           domain.MarketOrder
			next, runAt *time.Time
		)
		if err := rows.Scan(&o.ID, &o.GameID, &o.Resource, &o.ResourceName, &o.Budget, &o.MaxPrice,
			&o.Every, &o.Spent, &o.Bought, &next, &runAt, &o.Result, &o.AddedAt); err != nil {
			return nil, err
		}
		if next != nil {
			o.NextAt = *next
		}
		if runAt != nil {
			o.RunAt = *runAt
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Put ставит заявку на ресурс. Прежняя заявка на тот же ресурс заменяется
// целиком, вместе с потраченным: новая сумма — новый счёт. Срок — сразу,
// воркер заберёт её на ближайшей проверке.
func (r *MarketOrders) Put(ctx context.Context, o domain.MarketOrder, actorID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_market_orders
		        (game_id, resource_id, resource, budget, max_price, every_min, next_at, added_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (game_id, resource_id) DO UPDATE
		    SET resource = EXCLUDED.resource, budget = EXCLUDED.budget,
		        max_price = EXCLUDED.max_price, every_min = EXCLUDED.every_min,
		        spent = 0, bought = 0, next_at = EXCLUDED.next_at,
		        run_at = NULL, result = '', added_by = EXCLUDED.added_by, added_at = now()`,
		o.GameID, o.Resource, o.ResourceName, o.Budget, o.MaxPrice, o.Every, o.NextAt, actorID)
	return err
}

// List — заявки партии по ресурсу.
func (r *MarketOrders) List(ctx context.Context, gameID string) ([]domain.MarketOrder, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+marketOrderColumns+` FROM supremacy_market_orders
		  WHERE game_id = $1 ORDER BY resource_id`, gameID)
	if err != nil {
		return nil, err
	}
	return scanMarketOrders(rows)
}

// Due — открытые заявки, у которых подошёл срок.
func (r *MarketOrders) Due(ctx context.Context, now time.Time) ([]domain.MarketOrder, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+marketOrderColumns+` FROM supremacy_market_orders
		  WHERE next_at IS NOT NULL AND next_at <= $1 ORDER BY next_at`, now)
	if err != nil {
		return nil, err
	}
	return scanMarketOrders(rows)
}

// Open — открытые заявки партии: раз уж зашли, смотрим биржу для всех.
func (r *MarketOrders) Open(ctx context.Context, gameID string) ([]domain.MarketOrder, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+marketOrderColumns+` FROM supremacy_market_orders
		  WHERE game_id = $1 AND next_at IS NOT NULL ORDER BY id`, gameID)
	if err != nil {
		return nil, err
	}
	return scanMarketOrders(rows)
}

// Mark записывает итог захода: сколько потратили и купили сверх прежнего
// и следующий срок. Нулевой срок заявку закрывает.
func (r *MarketOrders) Mark(ctx context.Context, id int64, at time.Time, result string,
	spent, bought int, next time.Time) error {

	var nextAt *time.Time
	if !next.IsZero() {
		nextAt = &next
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE supremacy_market_orders
		    SET run_at = $2, result = $3, spent = spent + $4, bought = bought + $5, next_at = $6
		  WHERE id = $1`,
		id, at, result, spent, bought, nextAt)
	return err
}

// Remove убирает заявку партии — открытую или закрытую.
func (r *MarketOrders) Remove(ctx context.Context, gameID string, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM supremacy_market_orders WHERE id = $1 AND game_id = $2`, id, gameID)
	return err
}
