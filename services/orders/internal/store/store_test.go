package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil/pgxtest"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/orders/migrations"
)

func newStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	url := pgxtest.StartPostgres(t) // skips under -short or without Docker
	ctx := context.Background()
	if err := pgxutil.Migrate(ctx, url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxutil.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

func TestCRUD(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	id, user := uuid.New(), uuid.New()
	lines := []service.Line{{SKU: "B-1", Quantity: 2, UnitPriceCents: 30}, {SKU: "A-1", Quantity: 1, UnitPriceCents: 40}}

	created, err := s.Create(ctx, service.Order{ID: id, UserID: user, TotalCents: 100, Lines: lines})
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at not populated")
	}

	got, err := s.Get(ctx, id)
	if err != nil || got.UserID != user || got.TotalCents != 100 {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if len(got.Lines) != 2 || got.Lines[0] != lines[0] || got.Lines[1] != lines[1] {
		t.Fatalf("lines = %+v, want %+v in order", got.Lines, lines)
	}

	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM orders WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	other := uuid.New()
	// Total and lines passed to Update are ignored: only the user changes.
	upd, err := s.Update(ctx, service.Order{ID: id, UserID: other, TotalCents: 7, Lines: []service.Line{{SKU: "X", Quantity: 1}}})
	if err != nil || upd.UserID != other || upd.TotalCents != 100 || len(upd.Lines) != 2 || !upd.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM orders WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.After(before) {
		t.Errorf("updated_at not bumped: %v -> %v", before, after)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); err != service.ErrNotFound {
		t.Errorf("get after delete = %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_lines WHERE order_id=$1`, id).Scan(&n); err != nil || n != 0 {
		t.Errorf("lines left after delete: %d, %v", n, err)
	}
	if err := s.Delete(ctx, id); err != service.ErrNotFound {
		t.Errorf("delete missing = %v", err)
	}
	if _, err := s.Update(ctx, service.Order{ID: id, UserID: user}); err != service.ErrNotFound {
		t.Errorf("update missing = %v", err)
	}
}

func TestCheckConstraints(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, service.Order{ID: uuid.New(), UserID: uuid.New(), TotalCents: -1}); err == nil {
		t.Fatal("negative total accepted on create")
	}
	// A bad line must roll back the order row too.
	id := uuid.New()
	if _, err := s.Create(ctx, service.Order{ID: id, UserID: uuid.New(), TotalCents: 1,
		Lines: []service.Line{{SKU: "OK-1", Quantity: 1}, {SKU: "BAD-1", Quantity: 0}}}); err == nil {
		t.Fatal("zero-quantity line accepted")
	}
	if _, err := s.Get(ctx, id); err != service.ErrNotFound {
		t.Errorf("order survived a failed line insert: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_lines`).Scan(&n); err != nil || n != 0 {
		t.Errorf("stray lines: %d, %v", n, err)
	}
}

func TestListLoadsLines(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	user := uuid.New()
	for i := 0; i < 3; i++ {
		lines := make([]service.Line, i+1)
		for j := range lines {
			lines[j] = service.Line{SKU: "S-" + string(rune('A'+j)), Quantity: int32(j + 1), UnitPriceCents: 10}
		}
		if _, err := s.Create(ctx, service.Order{ID: uuid.New(), UserID: user, TotalCents: 1, Lines: lines}); err != nil {
			t.Fatal(err)
		}
	}
	// An order with no lines (placed before lines existed) lists fine.
	if _, err := s.Create(ctx, service.Order{ID: uuid.New(), UserID: user, TotalCents: 5}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, service.ListParams{UserID: &user, Limit: 10})
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	counts := map[int]bool{}
	for _, r := range rows {
		counts[len(r.Lines)] = true
	}
	if !counts[0] || !counts[1] || !counts[2] || !counts[3] {
		t.Errorf("line counts = %v, want 0,1,2,3", counts)
	}
}

// walk pages through the store the way the service does and returns ids in order.
func walk(t *testing.T, s *store.Store, user *uuid.UUID, size int) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	p := service.ListParams{UserID: user, Limit: size + 1}
	for pages := 0; pages < 100; pages++ {
		rows, err := s.List(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		more := len(rows) > size
		if more {
			rows = rows[:size]
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		if !more {
			return ids
		}
		last := rows[len(rows)-1]
		// Round-trip through the real page token to exercise its precision.
		at, id, err := pagination.Decode(pagination.Encode(last.CreatedAt, last.ID))
		if err != nil {
			t.Fatal(err)
		}
		p.HasCursor, p.AfterCreatedAt, p.AfterID = true, at, id
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func TestKeysetPaginationWithTies(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	// 12 rows share one created_at; 3 more are newer; bob owns some of each.
	tie := time.Date(2026, 1, 1, 12, 0, 0, 123456000, time.UTC)
	insert := func(user uuid.UUID, at time.Time) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx,
			`INSERT INTO orders (id, user_id, total_cents, created_at) VALUES ($1,$2,1,$3)`, id, user, at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	wantAlice := map[uuid.UUID]bool{}
	for i := 0; i < 8; i++ {
		wantAlice[insert(alice, tie)] = true
	}
	for i := 0; i < 4; i++ {
		insert(bob, tie)
	}
	for i := 0; i < 3; i++ {
		wantAlice[insert(alice, tie.Add(time.Duration(i+1)*time.Minute))] = true
	}
	insert(bob, tie.Add(time.Hour))

	check := func(name string, user *uuid.UUID, want, size int) {
		t.Run(name, func(t *testing.T) {
			ids := walk(t, s, user, size)
			if len(ids) != want {
				t.Fatalf("got %d rows, want %d", len(ids), want)
			}
			seen := map[uuid.UUID]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Fatalf("duplicate %s", id)
				}
				seen[id] = true
			}
			if user != nil && *user == alice {
				for id := range wantAlice {
					if !seen[id] {
						t.Fatalf("missing %s", id)
					}
				}
			}
		})
	}
	check("all, size 1", nil, 16, 1)
	check("all, size 5", nil, 16, 5)
	check("all, size 100", nil, 16, 100)
	check("alice, size 3", &alice, 11, 3)
	check("alice, size 4", &alice, 11, 4)
	check("bob, size 2", &bob, 5, 2)

	// Order is strictly (created_at desc, id desc).
	rows, err := s.List(ctx, service.ListParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.CreatedAt.Before(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID.String() < b.ID.String()) {
			t.Fatalf("rows %d/%d out of order", i-1, i)
		}
	}
}
