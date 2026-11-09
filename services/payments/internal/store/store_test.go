package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil/pgxtest"
	"github.com/francispowlesland/microservices-app-go/services/payments/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/payments/migrations"
)

const (
	succeeded = paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
	failed    = paymentsv1.PaymentStatus_PAYMENT_STATUS_FAILED
	refunded  = paymentsv1.PaymentStatus_PAYMENT_STATUS_REFUNDED
)

// newStore starts a throwaway Postgres (skipped under -short or without Docker),
// applies the real migrations and returns a Store and its pool.
func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := pgxtest.StartPostgres(t)
	ctx := context.Background()
	if err := pgxutil.Migrate(ctx, url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxutil.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

func newPayment(createdBy, order uuid.UUID, key string, amount int64) service.NewPayment {
	return service.NewPayment{
		ID: uuid.New(), OrderID: order, AmountCents: amount, Currency: "USD",
		Status: succeeded, CreatedBy: createdBy, IdempotencyKey: key,
	}
}

func TestInsertGetAndReplay(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	user, order := uuid.New(), uuid.New()

	first, created, err := s.Insert(ctx, newPayment(user, order, "k", 500))
	if err != nil || !created {
		t.Fatalf("first insert: created=%v err=%v", created, err)
	}
	if first.Currency != "USD" || first.AmountCents != 500 || first.Status != succeeded {
		t.Fatalf("round trip: %+v", first)
	}

	replay, created, err := s.Insert(ctx, newPayment(user, order, "k", 500))
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("replay must return original: created=%v id=%v want %v err=%v", created, replay.ID, first.ID, err)
	}

	// Same key, different parameters: the store hands back the original row so the
	// service can reject the mismatch; it must not overwrite or add anything.
	other, created, err := s.Insert(ctx, newPayment(user, order, "k", 999))
	if err != nil || created || other.ID != first.ID || other.AmountCents != 500 {
		t.Fatalf("mismatched replay: %+v created=%v err=%v", other, created, err)
	}

	// Keys are scoped per caller.
	if p, created, err := s.Insert(ctx, newPayment(uuid.New(), order, "k", 500)); err != nil || !created || p.ID == first.ID {
		t.Fatalf("other user same key: %+v %v %v", p, created, err)
	}

	got, err := s.Get(ctx, first.ID)
	if err != nil || got.ID != first.ID || got.CreatedBy != user {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get(ctx, uuid.New()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestInsertConcurrentSameKeyCreatesOneRow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	user, order := uuid.New(), uuid.New()

	const n = 20
	var created atomic.Int32
	ids := make(chan uuid.UUID, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, c, err := s.Insert(ctx, newPayment(user, order, "same", 100))
			if err != nil {
				t.Errorf("insert: %v", err)
				return
			}
			if c {
				created.Add(1)
			}
			ids <- p.ID
		}()
	}
	close(start)
	wg.Wait()
	close(ids)

	if created.Load() != 1 {
		t.Fatalf("created = %d, want exactly 1", created.Load())
	}
	distinct := map[uuid.UUID]bool{}
	for id := range ids {
		distinct[id] = true
	}
	if len(distinct) != 1 {
		t.Fatalf("callers saw %d distinct payments, want 1", len(distinct))
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE created_by = $1`, user).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows = %d err=%v", rows, err)
	}
}

func TestTransitionConcurrentRefundsOneWins(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, _, err := s.Insert(ctx, newPayment(uuid.New(), uuid.New(), "k", 100))
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Transition(ctx, p.ID, succeeded, refunded, "dup")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, service.ErrStateConflict):
				conflict.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != n-1 {
		t.Fatalf("ok=%d conflict=%d, want 1 and %d", ok.Load(), conflict.Load(), n-1)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Status != refunded || got.RefundReason != "dup" || !got.UpdatedAt.After(got.CreatedAt) {
		t.Fatalf("after refund: %+v", got)
	}
}

func TestTransitionErrors(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, err := s.Transition(ctx, uuid.New(), succeeded, refunded, ""); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	n := newPayment(uuid.New(), uuid.New(), "k", 100)
	n.Status, n.FailureReason = failed, "amount exceeds limit"
	p, _, _ := s.Insert(ctx, n)
	if _, err := s.Transition(ctx, p.ID, succeeded, refunded, ""); !errors.Is(err, service.ErrStateConflict) {
		t.Fatalf("FAILED -> REFUNDED must conflict: %v", err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Status != failed || got.FailureReason != "amount exceeds limit" {
		t.Fatalf("row changed: %+v", got)
	}
}

func TestLedgerImmutability(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, _, err := s.Insert(ctx, newPayment(uuid.New(), uuid.New(), "k", 100))
	if err != nil {
		t.Fatal(err)
	}

	for name, q := range map[string]string{
		"amount":     `UPDATE payments SET amount_cents = 1 WHERE id = $1`,
		"currency":   `UPDATE payments SET currency = 'EUR' WHERE id = $1`,
		"order":      `UPDATE payments SET order_id = gen_random_uuid() WHERE id = $1`,
		"created_by": `UPDATE payments SET created_by = gen_random_uuid() WHERE id = $1`,
		"delete":     `DELETE FROM payments WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := pool.Exec(ctx, q, p.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23001" { // restrict_violation, raised by payments_guard
				t.Fatalf("expected ledger trigger rejection (23001), got %v", err)
			}
		})
	}

	// Whole-row rewrite that keeps the protected columns is still allowed (status path).
	if _, err := pool.Exec(ctx, `UPDATE payments SET failure_reason = failure_reason WHERE id = $1`, p.ID); err != nil {
		t.Fatalf("non-protected update must work: %v", err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.AmountCents != 100 || got.Currency != "USD" {
		t.Fatalf("ledger row changed: %+v", got)
	}
}

func TestConstraints(t *testing.T) {
	_, pool := newStore(t)
	ctx := context.Background()
	for name, q := range map[string]string{
		"zero amount": `INSERT INTO payments (id, order_id, amount_cents, currency, status, created_by, idempotency_key)
			VALUES (gen_random_uuid(), gen_random_uuid(), 0, 'USD', 2, gen_random_uuid(), 'k')`,
		"unspecified status": `INSERT INTO payments (id, order_id, amount_cents, currency, status, created_by, idempotency_key)
			VALUES (gen_random_uuid(), gen_random_uuid(), 1, 'USD', 0, gen_random_uuid(), 'k')`,
		"status out of range": `INSERT INTO payments (id, order_id, amount_cents, currency, status, created_by, idempotency_key)
			VALUES (gen_random_uuid(), gen_random_uuid(), 1, 'USD', 5, gen_random_uuid(), 'k')`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, q); err == nil {
				t.Fatal("expected check violation")
			}
		})
	}
}

func TestListFiltersAndKeysetPagination(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	orderA, orderB := uuid.New(), uuid.New()

	// Eight rows, newest last inserted. Two share a created_at only via ordering by id,
	// which Postgres timestamps (microseconds) make unlikely; ordering is still total.
	var all []service.Payment
	plan := []struct {
		user  uuid.UUID
		order uuid.UUID
	}{{alice, orderA}, {alice, orderA}, {alice, orderB}, {bob, orderA}, {alice, orderA}, {bob, orderB}, {alice, orderB}, {alice, orderA}}
	for i, c := range plan {
		p, _, err := s.Insert(ctx, newPayment(c.user, c.order, "k"+string(rune('a'+i)), int64(10+i)))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
		time.Sleep(2 * time.Millisecond)
	}

	walk := func(f service.ListFilter, size int) []service.Payment {
		var out []service.Payment
		for {
			f.Limit = size + 1
			rows, err := s.List(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			more := len(rows) > size
			if more {
				rows = rows[:size]
			}
			out = append(out, rows...)
			if !more {
				return out
			}
			last := rows[len(rows)-1]
			// Round-trip through the real token codec, as the service does.
			at, id, err := pagination.Decode(pagination.Encode(last.CreatedAt, last.ID))
			if err != nil {
				t.Fatal(err)
			}
			f.HasCursor, f.AfterCreatedAt, f.AfterID = true, at, id
		}
	}
	ids := func(ps []service.Payment) []uuid.UUID {
		var r []uuid.UUID
		for _, p := range ps {
			r = append(r, p.ID)
		}
		return r
	}
	want := func(match func(service.Payment) bool) []uuid.UUID {
		var r []uuid.UUID
		for i := len(all) - 1; i >= 0; i-- { // newest first
			if match(all[i]) {
				r = append(r, all[i].ID)
			}
		}
		return r
	}
	equal := func(a, b []uuid.UUID) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	tests := []struct {
		name  string
		f     service.ListFilter
		match func(service.Payment) bool
	}{
		{"admin, all", service.ListFilter{}, func(service.Payment) bool { return true }},
		{"owner only", service.ListFilter{CreatedBy: &alice}, func(p service.Payment) bool { return p.CreatedBy == alice }},
		{"admin, by order", service.ListFilter{OrderID: &orderA}, func(p service.Payment) bool { return p.OrderID == orderA }},
		{"owner and order", service.ListFilter{CreatedBy: &bob, OrderID: &orderB}, func(p service.Payment) bool { return p.CreatedBy == bob && p.OrderID == orderB }},
	}
	for _, tc := range tests {
		for _, size := range []int{1, 3, 100} {
			got := ids(walk(tc.f, size))
			if w := want(tc.match); !equal(got, w) {
				t.Errorf("%s size=%d: got %d rows, want %d (order or content differs)", tc.name, size, len(got), len(w))
			}
		}
	}
	if rows, err := s.List(ctx, service.ListFilter{CreatedBy: ptr(uuid.New()), Limit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("stranger: %d rows err=%v", len(rows), err)
	}
}

func ptr[T any](v T) *T { return &v }
