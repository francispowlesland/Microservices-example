// Package store is the pgx implementation of service.Store.
package store

import (
	"context"

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

const cols = "id, sku, quantity, created_at"

func scan(row pgx.Row) (service.Item, error) {
	var it service.Item
	err := row.Scan(&it.ID, &it.SKU, &it.Quantity, &it.CreatedAt)
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
		`INSERT INTO inventory_items (id, sku, quantity) VALUES ($1, $2, $3) RETURNING `+cols,
		it.ID, it.SKU, it.Quantity))
	return out, mapErr(err)
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (service.Item, error) {
	out, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+cols+` FROM inventory_items WHERE id = $1`, id))
	return out, mapErr(err)
}

func (s *Store) Update(ctx context.Context, id uuid.UUID, sku string, quantity int32) (service.Item, error) {
	out, err := scan(s.pool.QueryRow(ctx,
		`UPDATE inventory_items SET sku = $2, quantity = $3, updated_at = now() WHERE id = $1 RETURNING `+cols,
		id, sku, quantity))
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
