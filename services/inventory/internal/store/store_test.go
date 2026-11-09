package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil/pgxtest"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/inventory/migrations"
)

func setup(t *testing.T) (*store.Store, *pgxpool.Pool) {
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
	st, pool := setup(t)
	ctx := context.Background()

	id := uuid.New()
	created, err := st.Create(ctx, service.Item{ID: id, SKU: "CRUD-1", Quantity: 5})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != id || created.SKU != "CRUD-1" || created.Quantity != 5 || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}

	got, err := st.Get(ctx, id)
	if err != nil || got.ID != created.ID || got.SKU != created.SKU || got.Quantity != created.Quantity || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("get = %+v, %v; want %+v", got, err, created)
	}

	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM inventory_items WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	upd, err := st.Update(ctx, id, "CRUD-2", 9, 1250)
	if err != nil || upd.SKU != "CRUD-2" || upd.Quantity != 9 || upd.PriceCents != 1250 || !upd.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update = %+v, %v", upd, err)
	}
	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM inventory_items WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.After(before) {
		t.Errorf("updated_at not bumped: before=%v after=%v", before, after)
	}

	if err := st.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, id); err != service.ErrNotFound {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestNotFound(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	missing := uuid.New()
	if _, err := st.Get(ctx, missing); err != service.ErrNotFound {
		t.Errorf("get = %v", err)
	}
	if _, err := st.Update(ctx, missing, "NOPE-1", 1, 0); err != service.ErrNotFound {
		t.Errorf("update = %v", err)
	}
	if err := st.Delete(ctx, missing); err != service.ErrNotFound {
		t.Errorf("delete = %v", err)
	}
}

func TestUniqueSKU(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	a, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: "DUP-1", Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: "DUP-2", Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: "DUP-1", Quantity: 1}); err != service.ErrDuplicateSKU {
		t.Errorf("create dup = %v, want ErrDuplicateSKU", err)
	}
	if _, err := st.Update(ctx, b.ID, "DUP-1", 1, 0); err != service.ErrDuplicateSKU {
		t.Errorf("update onto existing sku = %v, want ErrDuplicateSKU", err)
	}
	if _, err := st.Update(ctx, a.ID, "DUP-1", 3, 0); err != nil {
		t.Errorf("update keeping own sku = %v", err)
	}
}

// Case-differing SKUs are folded by the service before they reach the store
// (see service.NormaliseSKU), so the store sees identical strings. This test
// pins that pipeline: two inputs differing only in case collide.
func TestUniqueSKUCaseInsensitiveAtInput(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	first, ok1 := service.NormaliseSKU("Widget-1")
	second, ok2 := service.NormaliseSKU("widget-1")
	if !ok1 || !ok2 || first != second {
		t.Fatalf("normalise: %q/%v vs %q/%v", first, ok1, second, ok2)
	}
	if _, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: first, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: second, Quantity: 1}); err != service.ErrDuplicateSKU {
		t.Errorf("err = %v, want ErrDuplicateSKU", err)
	}
}

func TestQuantityCheckConstraint(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	if _, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: "NEG-1", Quantity: -1}); err == nil {
		t.Fatal("create with negative quantity succeeded")
	} else if err == service.ErrDuplicateSKU || err == service.ErrNotFound {
		t.Fatalf("check violation mis-mapped: %v", err)
	}
	it, err := st.Create(ctx, service.Item{ID: uuid.New(), SKU: "NEG-2", Quantity: 0})
	if err != nil {
		t.Fatalf("zero quantity: %v", err)
	}
	if _, err := st.Update(ctx, it.ID, "NEG-2", -1, 0); err == nil {
		t.Fatal("update to negative quantity succeeded")
	}
	got, err := st.Get(ctx, it.ID)
	if err != nil || got.Quantity != 0 {
		t.Errorf("row changed after rejected update: %+v, %v", got, err)
	}
}

// TestListKeysetAcrossTies inserts rows sharing identical created_at values and
// pages through them with a page size that splits a tie group.
func TestListKeysetAcrossTies(t *testing.T) {
	st, pool := setup(t)
	ctx := context.Background()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	want := map[uuid.UUID]bool{}
	// 3 distinct timestamps x 4 rows each = 12 rows, 4-way ties.
	for g := 0; g < 3; g++ {
		ts := base.Add(time.Duration(g) * time.Minute)
		for i := 0; i < 4; i++ {
			id := uuid.New()
			want[id] = true
			if _, err := pool.Exec(ctx,
				`INSERT INTO inventory_items (id, sku, quantity, created_at) VALUES ($1, $2, 1, $3)`,
				id, "TIE-"+id.String(), ts); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, size := range []int{1, 3, 5, 11, 12} {
		var (
			got    []service.Item
			cursor *service.Cursor
		)
		for guard := 0; ; guard++ {
			if guard > 20 {
				t.Fatalf("size %d: no termination", size)
			}
			// Mirror the service: fetch size+1 to detect another page.
			rows, err := st.List(ctx, cursor, size+1)
			if err != nil {
				t.Fatal(err)
			}
			more := len(rows) > size
			if more {
				rows = rows[:size]
			}
			got = append(got, rows...)
			if !more {
				break
			}
			last := rows[len(rows)-1]
			cursor = &service.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		if len(got) != len(want) {
			t.Fatalf("size %d: got %d rows, want %d", size, len(got), len(want))
		}
		seen := map[uuid.UUID]bool{}
		for i, it := range got {
			if seen[it.ID] || !want[it.ID] {
				t.Fatalf("size %d: duplicate or unknown row %s", size, it.ID)
			}
			seen[it.ID] = true
			if i == 0 {
				continue
			}
			prev := got[i-1]
			ordered := prev.CreatedAt.After(it.CreatedAt) ||
				(prev.CreatedAt.Equal(it.CreatedAt) && prev.ID.String() > it.ID.String())
			if !ordered {
				t.Fatalf("size %d: row %d out of order (%v,%s) after (%v,%s)", size, i, it.CreatedAt, it.ID, prev.CreatedAt, prev.ID)
			}
		}
	}
}

func TestListEmpty(t *testing.T) {
	st, _ := setup(t)
	items, err := st.List(context.Background(), nil, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func seedItem(t *testing.T, st *store.Store, sku string, qty int32, price int64) {
	t.Helper()
	if _, err := st.Create(context.Background(), service.Item{ID: uuid.New(), SKU: sku, Quantity: qty, PriceCents: price}); err != nil {
		t.Fatal(err)
	}
}

func qtyOf(t *testing.T, pool *pgxpool.Pool, sku string) int32 {
	t.Helper()
	var q int32
	if err := pool.QueryRow(context.Background(), `SELECT quantity FROM inventory_items WHERE sku=$1`, sku).Scan(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestReserveAndRelease(t *testing.T) {
	st, pool := setup(t)
	ctx := context.Background()
	seedItem(t, st, "RA-1", 5, 300)
	seedItem(t, st, "RB-1", 1, 700)

	got, err := st.Reserve(ctx, []service.StockLine{{SKU: "RB-1", Quantity: 1}, {SKU: "RA-1", Quantity: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SKU != "RB-1" || got[0].UnitPriceCents != 700 || got[1].SKU != "RA-1" || got[1].UnitPriceCents != 300 {
		t.Fatalf("lines = %+v", got)
	}
	if qtyOf(t, pool, "RA-1") != 1 || qtyOf(t, pool, "RB-1") != 0 {
		t.Fatal("stock not decremented")
	}

	// Short on the second line (in SKU order RB-1 is locked after RA-1): nothing may change.
	_, err = st.Reserve(ctx, []service.StockLine{{SKU: "RA-1", Quantity: 1}, {SKU: "RB-1", Quantity: 1}})
	if !errors.Is(err, service.ErrInsufficientStock) {
		t.Fatalf("err = %v, want ErrInsufficientStock", err)
	}
	if qtyOf(t, pool, "RA-1") != 1 {
		t.Errorf("RA-1 = %d after rolled-back reserve, want 1", qtyOf(t, pool, "RA-1"))
	}
	if _, err := st.Reserve(ctx, []service.StockLine{{SKU: "RA-1", Quantity: 1}, {SKU: "NOPE-1", Quantity: 1}}); !errors.Is(err, service.ErrUnknownSKU) {
		t.Fatalf("err = %v, want ErrUnknownSKU", err)
	}
	if qtyOf(t, pool, "RA-1") != 1 {
		t.Error("RA-1 changed by a reserve that failed on an unknown SKU")
	}

	if err := st.Release(ctx, []service.StockLine{{SKU: "RA-1", Quantity: 4}, {SKU: "RB-1", Quantity: 1}, {SKU: "GONE-1", Quantity: 9}}); err != nil {
		t.Fatal(err)
	}
	if qtyOf(t, pool, "RA-1") != 5 || qtyOf(t, pool, "RB-1") != 1 {
		t.Errorf("after release = %d/%d, want 5/1", qtyOf(t, pool, "RA-1"), qtyOf(t, pool, "RB-1"))
	}
}

// Ten buyers race for five units: exactly five win and stock never goes negative.
func TestReserveConcurrentNeverOversells(t *testing.T) {
	st, pool := setup(t)
	seedItem(t, st, "RACE-1", 5, 1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.Reserve(context.Background(), []service.StockLine{{SKU: "RACE-1", Quantity: 1}}); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 5 || qtyOf(t, pool, "RACE-1") != 0 {
		t.Errorf("won=%d stock=%d, want 5 and 0", won, qtyOf(t, pool, "RACE-1"))
	}
}

func TestPriceCheckConstraint(t *testing.T) {
	st, _ := setup(t)
	if _, err := st.Create(context.Background(), service.Item{ID: uuid.New(), SKU: "NEGP-1", PriceCents: -1}); err == nil {
		t.Fatal("negative price accepted")
	}
}
