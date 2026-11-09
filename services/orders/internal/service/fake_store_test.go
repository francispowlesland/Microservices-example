package service_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

// fakeStore is an in-memory service.Store. Each Create advances a fake clock by one
// second unless sameTime is set, in which case every row shares one created_at.
type fakeStore struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]service.Order
	clock    time.Time
	sameTime bool
	failWith error
	lastList service.ListParams
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[uuid.UUID]service.Order{}, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeStore) Create(_ context.Context, o service.Order) (service.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return service.Order{}, f.failWith
	}
	if !f.sameTime {
		f.clock = f.clock.Add(time.Second)
	}
	o.CreatedAt = f.clock
	f.rows[o.ID] = o
	return o, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (service.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return service.Order{}, f.failWith
	}
	o, ok := f.rows[id]
	if !ok {
		return service.Order{}, service.ErrNotFound
	}
	return o, nil
}

func (f *fakeStore) Update(_ context.Context, o service.Order) (service.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return service.Order{}, f.failWith
	}
	old, ok := f.rows[o.ID]
	if !ok {
		return service.Order{}, service.ErrNotFound
	}
	o.CreatedAt, o.TotalCents, o.Lines = old.CreatedAt, old.TotalCents, old.Lines
	f.rows[o.ID] = o
	return o, nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if _, ok := f.rows[id]; !ok {
		return service.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeStore) List(_ context.Context, p service.ListParams) ([]service.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastList = p
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []service.Order
	for _, o := range f.rows {
		if p.UserID != nil && o.UserID != *p.UserID {
			continue
		}
		if p.HasCursor && !before(o, p.AfterCreatedAt, p.AfterID) {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

// before reports (o.CreatedAt, o.ID) < (at, id).
func before(o service.Order, at time.Time, id uuid.UUID) bool {
	if !o.CreatedAt.Equal(at) {
		return o.CreatedAt.Before(at)
	}
	return o.ID.String() < id.String()
}

var errBoom = errors.New("pq: connection refused at 10.0.0.1")

// fakeInv is an in-memory service.Inventory with the same all-or-nothing Reserve as the real one.
type fakeInv struct {
	mu       sync.Mutex
	stock    map[string]int32
	price    map[string]int64
	released [][]service.Line
	failWith error // returned by Reserve
	relErr   error // returned by Release
}

func newFakeInv() *fakeInv {
	return &fakeInv{
		stock: map[string]int32{"WIDGET": 1000, "GADGET": 5},
		price: map[string]int64{"WIDGET": 1000, "GADGET": 250},
	}
}

func (f *fakeInv) Reserve(_ context.Context, lines []service.Line) ([]service.Line, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	out := make([]service.Line, len(lines))
	for i, l := range lines {
		have, ok := f.stock[l.SKU]
		if !ok {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("%s: unknown sku", l.SKU))
		}
		if have < l.Quantity {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s: insufficient stock", l.SKU))
		}
		l.UnitPriceCents = f.price[l.SKU]
		out[i] = l
	}
	for _, l := range lines {
		f.stock[l.SKU] -= l.Quantity
	}
	return out, nil
}

func (f *fakeInv) Release(_ context.Context, lines []service.Line) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.relErr != nil {
		return f.relErr
	}
	for _, l := range lines {
		f.stock[l.SKU] += l.Quantity
	}
	f.released = append(f.released, lines)
	return nil
}
