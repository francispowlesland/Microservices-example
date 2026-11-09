// Package store is the pgx implementation of service.Store.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/services/payments/internal/service"
)

// Store persists payments in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

var _ service.Store = (*Store)(nil)

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `id, order_id, amount_cents, currency, status, failure_reason, refund_reason,
	created_by, idempotency_key, created_at, updated_at`

func scan(row pgx.Row) (service.Payment, error) {
	var p service.Payment
	var st int16
	err := row.Scan(&p.ID, &p.OrderID, &p.AmountCents, &p.Currency, &st, &p.FailureReason,
		&p.RefundReason, &p.CreatedBy, &p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt)
	p.Status = paymentsv1.PaymentStatus(st)
	return p, err
}

// Insert implements service.Store. The insert and the read-back share one
// transaction; ON CONFLICT DO NOTHING makes a concurrent same-key insert wait
// for the first to commit, so exactly one row is ever created.
func (s *Store) Insert(ctx context.Context, n service.NewPayment) (service.Payment, bool, error) {
	var out service.Payment
	var created bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		p, err := scan(tx.QueryRow(ctx, `
			INSERT INTO payments (id, order_id, amount_cents, currency, status, failure_reason, created_by, idempotency_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (created_by, idempotency_key) DO NOTHING
			RETURNING `+columns,
			n.ID, n.OrderID, n.AmountCents, n.Currency, int16(n.Status), n.FailureReason, n.CreatedBy, n.IdempotencyKey))
		if err == nil {
			out, created = p, true
			return nil
		}
		if !pgxutil.IsNotFound(err) {
			return err
		}
		out, err = scan(tx.QueryRow(ctx,
			`SELECT `+columns+` FROM payments WHERE created_by = $1 AND idempotency_key = $2`,
			n.CreatedBy, n.IdempotencyKey))
		return err
	})
	if err != nil {
		return service.Payment{}, false, fmt.Errorf("store: insert payment: %w", err)
	}
	return out, created, nil
}

// Get implements service.Store.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (service.Payment, error) {
	p, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM payments WHERE id = $1`, id))
	if pgxutil.IsNotFound(err) {
		return service.Payment{}, service.ErrNotFound
	}
	if err != nil {
		return service.Payment{}, fmt.Errorf("store: get payment: %w", err)
	}
	return p, nil
}

// List implements service.Store. Ordering is (created_at DESC, id DESC).
func (s *Store) List(ctx context.Context, f service.ListFilter) ([]service.Payment, error) {
	// Each optional filter is a nullable parameter so the SQL text stays constant.
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM payments
		WHERE ($1::uuid IS NULL OR created_by = $1)
		  AND ($2::uuid IS NULL OR order_id = $2)
		  AND (NOT $3::boolean OR (created_at, id) < ($4::timestamptz, $5::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $6`,
		f.CreatedBy, f.OrderID, f.HasCursor, f.AfterCreatedAt, f.AfterID, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list payments: %w", err)
	}
	defer rows.Close()
	var out []service.Payment
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan payment: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list payments: %w", err)
	}
	return out, nil
}

// Transition implements service.Store. The row is locked with FOR UPDATE, so of
// several concurrent identical transitions exactly one succeeds.
func (s *Store) Transition(ctx context.Context, id uuid.UUID, from, to paymentsv1.PaymentStatus, refundReason string) (service.Payment, error) {
	var out service.Payment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM payments WHERE id = $1 FOR UPDATE`, id))
		if pgxutil.IsNotFound(err) {
			return service.ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.Status != from {
			return service.ErrStateConflict
		}
		out, err = scan(tx.QueryRow(ctx, `
			UPDATE payments SET status = $2, refund_reason = $3, updated_at = now()
			WHERE id = $1
			RETURNING `+columns, id, int16(to), refundReason))
		return err
	})
	if err != nil {
		if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrStateConflict) {
			return service.Payment{}, err
		}
		return service.Payment{}, fmt.Errorf("store: transition payment: %w", err)
	}
	return out, nil
}
