package service

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
)

// fakeStore is an in-memory Store. A single mutex stands in for row locking.
type fakeStore struct {
	mu     sync.Mutex
	rows   map[uuid.UUID]Payment
	clock  time.Time
	failOn string // method name that should return an error
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[uuid.UUID]Payment{}, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeStore) Insert(_ context.Context, n NewPayment) (Payment, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "Insert" {
		return Payment{}, false, errBoom
	}
	for _, p := range f.rows {
		if p.CreatedBy == n.CreatedBy && p.IdempotencyKey == n.IdempotencyKey {
			return p, false, nil
		}
	}
	f.clock = f.clock.Add(time.Second)
	p := Payment{
		ID: n.ID, OrderID: n.OrderID, AmountCents: n.AmountCents, Currency: n.Currency,
		Status: n.Status, FailureReason: n.FailureReason, CreatedBy: n.CreatedBy,
		IdempotencyKey: n.IdempotencyKey, CreatedAt: f.clock, UpdatedAt: f.clock,
	}
	f.rows[p.ID] = p
	return p, true, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "Get" {
		return Payment{}, errBoom
	}
	p, ok := f.rows[id]
	if !ok {
		return Payment{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) List(_ context.Context, fl ListFilter) ([]Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "List" {
		return nil, errBoom
	}
	var out []Payment
	for _, p := range f.rows {
		if fl.CreatedBy != nil && p.CreatedBy != *fl.CreatedBy {
			continue
		}
		if fl.OrderID != nil && p.OrderID != *fl.OrderID {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if fl.HasCursor {
		var rest []Payment
		for _, p := range out {
			if p.CreatedAt.Before(fl.AfterCreatedAt) ||
				(p.CreatedAt.Equal(fl.AfterCreatedAt) && p.ID.String() < fl.AfterID.String()) {
				rest = append(rest, p)
			}
		}
		out = rest
	}
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, nil
}

func (f *fakeStore) Transition(_ context.Context, id uuid.UUID, from, to paymentsv1.PaymentStatus, reason string) (Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "Transition" {
		return Payment{}, errBoom
	}
	p, ok := f.rows[id]
	if !ok {
		return Payment{}, ErrNotFound
	}
	if p.Status != from {
		return Payment{}, ErrStateConflict
	}
	p.Status, p.RefundReason = to, reason
	f.rows[id] = p
	return p, nil
}

type boomError struct{}

func (boomError) Error() string { return "pq: secret SQL detail" }

var errBoom = boomError{}
