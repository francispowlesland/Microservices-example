// Package store is the pgx implementation of service.Store. All SQL lives here.
package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

// Store is backed by a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

var _ service.Store = (*Store)(nil)

// New returns a Store using pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts the order and its lines in one transaction.
func (s *Store) Create(ctx context.Context, o service.Order) (service.Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return service.Order{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`INSERT INTO orders (id, user_id, total_cents) VALUES ($1, $2, $3) RETURNING created_at`,
		o.ID, o.UserID, o.TotalCents).Scan(&o.CreatedAt)
	if err != nil {
		return service.Order{}, err
	}
	for i, l := range o.Lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_lines (order_id, position, sku, quantity, unit_price_cents) VALUES ($1, $2, $3, $4, $5)`,
			o.ID, i, l.SKU, l.Quantity, l.UnitPriceCents); err != nil {
			return service.Order{}, err
		}
	}
	return o, tx.Commit(ctx)
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (service.Order, error) {
	var o service.Order
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, total_cents, created_at FROM orders WHERE id = $1`, id).
		Scan(&o.ID, &o.UserID, &o.TotalCents, &o.CreatedAt)
	if pgxutil.IsNotFound(err) {
		return service.Order{}, service.ErrNotFound
	}
	if err != nil {
		return service.Order{}, err
	}
	orders := []service.Order{o}
	if err := s.loadLines(ctx, orders); err != nil {
		return service.Order{}, err
	}
	return orders[0], nil
}

// Update only reassigns the order's user; total and lines are fixed once placed.
func (s *Store) Update(ctx context.Context, o service.Order) (service.Order, error) {
	err := s.pool.QueryRow(ctx,
		`UPDATE orders SET user_id = $2, updated_at = now()
		 WHERE id = $1 RETURNING total_cents, created_at`,
		o.ID, o.UserID).Scan(&o.TotalCents, &o.CreatedAt)
	if pgxutil.IsNotFound(err) {
		return service.Order{}, service.ErrNotFound
	}
	if err != nil {
		return service.Order{}, err
	}
	o.Lines = nil // the caller's lines are ignored; return what is stored
	orders := []service.Order{o}
	if err := s.loadLines(ctx, orders); err != nil {
		return service.Order{}, err
	}
	return orders[0], nil
}

// loadLines fills Lines on every order in one query.
func (s *Store) loadLines(ctx context.Context, orders []service.Order) error {
	ids := make([]uuid.UUID, len(orders))
	at := make(map[uuid.UUID]int, len(orders))
	for i, o := range orders {
		ids[i], at[o.ID] = o.ID, i
	}
	rows, err := s.pool.Query(ctx,
		`SELECT order_id, sku, quantity, unit_price_cents FROM order_lines
		  WHERE order_id = ANY($1) ORDER BY order_id, position`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			l  service.Line
		)
		if err := rows.Scan(&id, &l.SKU, &l.Quantity, &l.UnitPriceCents); err != nil {
			return err
		}
		orders[at[id]].Lines = append(orders[at[id]].Lines, l)
	}
	return rows.Err()
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM orders WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
}

// List returns up to p.Limit orders, newest first, ordered by (created_at, id) descending.
// A nil p.UserID and no cursor are expressed with NULL / false so a single static query serves
// every combination.
func (s *Store) List(ctx context.Context, p service.ListParams) ([]service.Order, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, total_cents, created_at
		   FROM orders
		  WHERE ($1::uuid IS NULL OR user_id = $1)
		    AND (NOT $2::boolean OR (created_at, id) < ($3::timestamptz, $4::uuid))
		  ORDER BY created_at DESC, id DESC
		  LIMIT $5`,
		p.UserID, p.HasCursor, p.AfterCreatedAt, p.AfterID, p.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.Order
	for rows.Next() {
		var o service.Order
		if err := rows.Scan(&o.ID, &o.UserID, &o.TotalCents, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	return out, s.loadLines(ctx, out)
}
