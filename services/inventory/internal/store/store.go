// Package store is the pgx implementation of service.Store.
package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/service"
)

const skuConstraint = "inventory_items_sku_key"

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ service.Store = (*Store)(nil)

const cols = "id, sku, quantity, price_cents, created_at"

func scan(row pgx.Row) (service.Item, error) {
	var it service.Item
	err := row.Scan(&it.ID, &it.SKU, &it.Quantity, &it.PriceCents, &it.CreatedAt)
	return it, err
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case pgxutil.IsNotFound(err):
		return service.ErrNotFound
	case pgxutil.IsUniqueViolation(err, skuConstraint):
		return service.ErrDuplicateSKU
	default:
		return err
	}
}

func (s *Store) Create(ctx context.Context, it service.Item) (service.Item, error) {
	out, err := scan(s.pool.QueryRow(ctx,
		`INSERT INTO inventory_items (id, sku, quantity, price_cents) VALUES ($1, $2, $3, $4) RETURNING `+cols,
		it.ID, it.SKU, it.Quantity, it.PriceCents))
	return out, mapErr(err)
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (service.Item, error) {
	out, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+cols+` FROM inventory_items WHERE id = $1`, id))
	return out, mapErr(err)
}

func (s *Store) Update(ctx context.Context, id uuid.UUID, sku string, quantity int32, priceCents int64) (service.Item, error) {
	out, err := scan(s.pool.QueryRow(ctx,
		`UPDATE inventory_items SET sku = $2, quantity = $3, price_cents = $4, updated_at = now() WHERE id = $1 RETURNING `+cols,
		id, sku, quantity, priceCents))
	return out, mapErr(err)
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM inventory_items WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, after *service.Cursor, limit int) ([]service.Item, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if after == nil {
		rows, err = s.pool.Query(ctx,
			`SELECT `+cols+` FROM inventory_items ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT `+cols+` FROM inventory_items
			 WHERE (created_at, id) < ($1, $2)
			 ORDER BY created_at DESC, id DESC LIMIT $3`, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []service.Item
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Reserve decrements every line inside one transaction. Lines are locked in SKU order so two
// concurrent reservations over the same SKUs cannot deadlock.
func (s *Store) Reserve(ctx context.Context, lines []service.StockLine) ([]service.StockLine, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	idx := make([]int, len(lines))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return lines[idx[a]].SKU < lines[idx[b]].SKU })

	out := make([]service.StockLine, len(lines))
	for _, i := range idx {
		l := lines[i]
		err := tx.QueryRow(ctx,
			`UPDATE inventory_items SET quantity = quantity - $2, updated_at = now()
			  WHERE sku = $1 AND quantity >= $2 RETURNING price_cents`,
			l.SKU, l.Quantity).Scan(&l.UnitPriceCents)
		if pgxutil.IsNotFound(err) {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inventory_items WHERE sku = $1)`, l.SKU).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, fmt.Errorf("%s: %w", l.SKU, service.ErrUnknownSKU)
			}
			return nil, fmt.Errorf("%s: %w", l.SKU, service.ErrInsufficientStock)
		}
		if err != nil {
			return nil, err
		}
		out[i] = l
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Release(ctx context.Context, lines []service.StockLine) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sorted := append([]service.StockLine(nil), lines...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].SKU < sorted[b].SKU })
	for _, l := range sorted {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items SET quantity = quantity + $2, updated_at = now() WHERE sku = $1`,
			l.SKU, l.Quantity); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
