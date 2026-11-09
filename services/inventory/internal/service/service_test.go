package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/service"
)

// fakeStore is an in-memory service.Store. It mirrors the real constraints:
// unique SKU and (created_at desc, id desc) ordering.
type fakeStore struct {
	mu    sync.Mutex
	items map[uuid.UUID]service.Item
	clock time.Time
	err   error // returned by every call when set
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		items: map[uuid.UUID]service.Item{},
		clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (f *fakeStore) skuTaken(sku string, except uuid.UUID) bool {
	for _, it := range f.items {
		if it.SKU == sku && it.ID != except {
			return true
		}
	}
	return false
}

func (f *fakeStore) Create(_ context.Context, it service.Item) (service.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return service.Item{}, f.err
	}
	if f.skuTaken(it.SKU, uuid.Nil) {
		return service.Item{}, service.ErrDuplicateSKU
	}
	f.clock = f.clock.Add(time.Second)
	it.CreatedAt = f.clock
	f.items[it.ID] = it
	return it, nil
}

func (f *fakeStore) Get(_ context.Context, id uuid.UUID) (service.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return service.Item{}, f.err
	}
	it, ok := f.items[id]
	if !ok {
		return service.Item{}, service.ErrNotFound
	}
	return it, nil
}

func (f *fakeStore) Update(_ context.Context, id uuid.UUID, sku string, qty int32) (service.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return service.Item{}, f.err
	}
	it, ok := f.items[id]
	if !ok {
		return service.Item{}, service.ErrNotFound
	}
	if f.skuTaken(sku, id) {
		return service.Item{}, service.ErrDuplicateSKU
	}
	it.SKU, it.Quantity = sku, qty
	f.items[id] = it
	return it, nil
}

func (f *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, ok := f.items[id]; !ok {
		return service.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func (f *fakeStore) List(_ context.Context, after *service.Cursor, limit int) ([]service.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	all := make([]service.Item, 0, len(f.items))
	for _, it := range f.items {
		all = append(all, it)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID.String() > all[j].ID.String()
	})
	var out []service.Item
	for _, it := range all {
		if after != nil {
			if it.CreatedAt.After(after.CreatedAt) ||
				(it.CreatedAt.Equal(after.CreatedAt) && it.ID.String() >= after.ID.String()) {
				continue
			}
		}
		out = append(out, it)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

var (
	adminCtx    = authn.NewContext(context.Background(), authn.Claims{Subject: uuid.NewString(), Roles: []string{"admin"}})
	customerCtx = authn.NewContext(context.Background(), authn.Claims{Subject: uuid.NewString(), Roles: []string{"customer"}})
)

func newSvc(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	return service.New(fs, slog.New(slog.NewTextHandler(io.Discard, nil))), fs
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (err=%v), want %v", got, err, want)
	}
}

func create(t *testing.T, s *service.Service, sku string, qty int32) *inventoryv1.InventoryItem {
	t.Helper()
	resp, err := s.CreateInventoryItem(adminCtx, &inventoryv1.CreateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Sku: sku, Quantity: qty},
	})
	if err != nil {
		t.Fatalf("create %q: %v", sku, err)
	}
	return resp.GetItem()
}

func TestRegistrationMatchesContract(t *testing.T) {
	s, _ := newSvc(t)
	srv := grpc.NewServer()
	inventoryv1.RegisterInventoryServiceServer(srv, s)
	info := srv.GetServiceInfo()[inventoryv1.InventoryService_ServiceDesc.ServiceName]
	got := map[string]bool{}
	for _, m := range info.Methods {
		if m.IsClientStream || m.IsServerStream {
			t.Errorf("%s: unexpected streaming", m.Name)
		}
		got[m.Name] = true
	}
	for _, m := range []string{"CreateInventoryItem", "GetInventoryItem", "UpdateInventoryItem", "DeleteInventoryItem", "ListInventoryItems"} {
		if !got[m] {
			t.Errorf("method %s not registered", m)
		}
	}
}

func TestAuthMatrix(t *testing.T) {
	s, _ := newSvc(t)
	seed := create(t, s, "SEED-1", 5)
	valid := &inventoryv1.InventoryItem{Id: seed.GetId(), Sku: "NEW-SKU", Quantity: 1}

	calls := map[string]func(ctx context.Context) error{
		"Create": func(ctx context.Context) error {
			_, err := s.CreateInventoryItem(ctx, &inventoryv1.CreateInventoryItemRequest{Item: &inventoryv1.InventoryItem{Sku: "C-1", Quantity: 1}})
			return err
		},
		"Get": func(ctx context.Context) error {
			_, err := s.GetInventoryItem(ctx, &inventoryv1.GetInventoryItemRequest{Id: seed.GetId()})
			return err
		},
		"Update": func(ctx context.Context) error {
			_, err := s.UpdateInventoryItem(ctx, &inventoryv1.UpdateInventoryItemRequest{Item: valid})
			return err
		},
		"Delete": func(ctx context.Context) error {
			_, err := s.DeleteInventoryItem(ctx, &inventoryv1.DeleteInventoryItemRequest{Id: seed.GetId()})
			return err
		},
		"List": func(ctx context.Context) error {
			_, err := s.ListInventoryItems(ctx, &inventoryv1.ListInventoryItemsRequest{})
			return err
		},
	}
	tests := []struct {
		rpc      string
		customer codes.Code
	}{
		{"Create", codes.PermissionDenied},
		{"Get", codes.OK},
		{"Update", codes.PermissionDenied},
		{"Delete", codes.PermissionDenied},
		{"List", codes.OK},
	}
	// Customer first so destructive admin calls cannot affect it.
	for _, tt := range tests {
		t.Run("customer/"+tt.rpc, func(t *testing.T) {
			wantCode(t, calls[tt.rpc](customerCtx), tt.customer)
		})
	}
	for _, tt := range tests {
		t.Run("admin/"+tt.rpc, func(t *testing.T) {
			wantCode(t, calls[tt.rpc](adminCtx), codes.OK)
		})
	}
}

func TestNormaliseSKU(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"upper", "ABC-1", "ABC-1", true},
		{"lower is upper-cased", "abc-1", "ABC-1", true},
		{"mixed with symbols", "aB.c_d-9", "AB.C_D-9", true},
		{"trimmed", "  sku1 \n", "SKU1", true},
		{"digit first", "1abc", "1ABC", true},
		{"max length 64", strings.Repeat("a", 64), strings.Repeat("A", 64), true},
		{"empty", "", "", false},
		{"blank", "   ", "", false},
		{"too long 65", strings.Repeat("A", 65), "", false},
		{"leading dot", ".ABC", "", false},
		{"leading dash", "-ABC", "", false},
		{"inner space", "AB C", "", false},
		{"slash", "AB/C", "", false},
		{"unicode dotless i", "ınventory", "", false},
		{"unicode letter", "ÄBC", "", false},
		{"semicolon", "AB;C", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := service.NormaliseSKU(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.wantOK, got)
			}
			if ok && got != tt.want {
				t.Fatalf("sku = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name string
		item *inventoryv1.InventoryItem
		want codes.Code
	}{
		{"nil item", nil, codes.InvalidArgument},
		{"empty sku", &inventoryv1.InventoryItem{Sku: "", Quantity: 1}, codes.InvalidArgument},
		{"long sku", &inventoryv1.InventoryItem{Sku: strings.Repeat("A", 65), Quantity: 1}, codes.InvalidArgument},
		{"bad chars", &inventoryv1.InventoryItem{Sku: "a b", Quantity: 1}, codes.InvalidArgument},
		{"negative quantity", &inventoryv1.InventoryItem{Sku: "OK-1", Quantity: -1}, codes.InvalidArgument},
		{"zero quantity ok", &inventoryv1.InventoryItem{Sku: "OK-2", Quantity: 0}, codes.OK},
		{"lower-case sku ok", &inventoryv1.InventoryItem{Sku: "ok-3", Quantity: 4}, codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSvc(t)
			resp, err := s.CreateInventoryItem(adminCtx, &inventoryv1.CreateInventoryItemRequest{Item: tt.item})
			wantCode(t, err, tt.want)
			if tt.want != codes.OK {
				return
			}
			if _, perr := uuid.Parse(resp.GetItem().GetId()); perr != nil {
				t.Errorf("id %q is not a UUID", resp.GetItem().GetId())
			}
			if want := strings.ToUpper(tt.item.GetSku()); resp.GetItem().GetSku() != want {
				t.Errorf("sku = %q, want %q", resp.GetItem().GetSku(), want)
			}
			if resp.GetItem().GetQuantity() != tt.item.GetQuantity() {
				t.Errorf("quantity = %d, want %d", resp.GetItem().GetQuantity(), tt.item.GetQuantity())
			}
		})
	}
}

func TestCreateIgnoresCallerSuppliedID(t *testing.T) {
	s, _ := newSvc(t)
	supplied := uuid.NewString()
	resp, err := s.CreateInventoryItem(adminCtx, &inventoryv1.CreateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Id: supplied, Sku: "X-1", Quantity: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetItem().GetId() == supplied {
		t.Fatal("service must generate the id, not accept the caller's")
	}
}

func TestInvalidUUIDs(t *testing.T) {
	s, _ := newSvc(t)
	for _, id := range []string{"", "not-a-uuid", "1234", "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz"} {
		t.Run("id="+id, func(t *testing.T) {
			_, err := s.GetInventoryItem(customerCtx, &inventoryv1.GetInventoryItemRequest{Id: id})
			wantCode(t, err, codes.InvalidArgument)
			_, err = s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{
				Item: &inventoryv1.InventoryItem{Id: id, Sku: "OK-1", Quantity: 1},
			})
			wantCode(t, err, codes.InvalidArgument)
			_, err = s.DeleteInventoryItem(adminCtx, &inventoryv1.DeleteInventoryItemRequest{Id: id})
			wantCode(t, err, codes.InvalidArgument)
		})
	}
	t.Run("update nil item", func(t *testing.T) {
		_, err := s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})
}

func TestNotFound(t *testing.T) {
	s, _ := newSvc(t)
	missing := uuid.NewString()
	_, err := s.GetInventoryItem(customerCtx, &inventoryv1.GetInventoryItemRequest{Id: missing})
	wantCode(t, err, codes.NotFound)
	_, err = s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Id: missing, Sku: "OK-1", Quantity: 1},
	})
	wantCode(t, err, codes.NotFound)
	_, err = s.DeleteInventoryItem(adminCtx, &inventoryv1.DeleteInventoryItemRequest{Id: missing})
	wantCode(t, err, codes.NotFound)
}

func TestDuplicateSKU(t *testing.T) {
	s, _ := newSvc(t)
	a := create(t, s, "AAA-1", 1)
	b := create(t, s, "BBB-1", 1)

	for _, sku := range []string{"AAA-1", "aaa-1", "  Aaa-1  "} {
		_, err := s.CreateInventoryItem(adminCtx, &inventoryv1.CreateInventoryItemRequest{
			Item: &inventoryv1.InventoryItem{Sku: sku, Quantity: 1},
		})
		wantCode(t, err, codes.AlreadyExists)
	}

	// Update b onto a's SKU (different case) conflicts.
	_, err := s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Id: b.GetId(), Sku: "aaa-1", Quantity: 1},
	})
	wantCode(t, err, codes.AlreadyExists)

	// Updating an item keeping its own SKU is fine.
	resp, err := s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Id: a.GetId(), Sku: "aaa-1", Quantity: 42},
	})
	if err != nil {
		t.Fatalf("self-update: %v", err)
	}
	if resp.GetItem().GetQuantity() != 42 || resp.GetItem().GetSku() != "AAA-1" {
		t.Errorf("update result = %v", resp.GetItem())
	}
}

func TestUpdateValidation(t *testing.T) {
	s, _ := newSvc(t)
	it := create(t, s, "UPD-1", 1)
	tests := []struct {
		name string
		sku  string
		qty  int32
	}{
		{"bad sku", "bad sku", 1},
		{"empty sku", "", 1},
		{"negative quantity", "UPD-1", -5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{
				Item: &inventoryv1.InventoryItem{Id: it.GetId(), Sku: tt.sku, Quantity: tt.qty},
			})
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGetUpdateDeleteRoundTrip(t *testing.T) {
	s, _ := newSvc(t)
	it := create(t, s, "rt-1", 3)
	got, err := s.GetInventoryItem(customerCtx, &inventoryv1.GetInventoryItemRequest{Id: it.GetId()})
	if err != nil || got.GetItem().GetSku() != "RT-1" || got.GetItem().GetQuantity() != 3 {
		t.Fatalf("get = %v, %v", got, err)
	}
	if _, err := s.DeleteInventoryItem(adminCtx, &inventoryv1.DeleteInventoryItemRequest{Id: it.GetId()}); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetInventoryItem(customerCtx, &inventoryv1.GetInventoryItemRequest{Id: it.GetId()})
	wantCode(t, err, codes.NotFound)
}

func TestListPagination(t *testing.T) {
	s, _ := newSvc(t)
	const n = 5
	for i := 0; i < n; i++ {
		create(t, s, "P-"+string(rune('A'+i)), int32(i))
	}

	tests := []struct {
		name     string
		pageSize int32
		pages    int
	}{
		{"size 2 -> 3 pages", 2, 3},
		{"size 5 -> 1 page", 5, 1},
		{"size 1 -> 5 pages", 1, 5},
		{"default size -> 1 page", 0, 1},
		{"negative -> default", -3, 1},
		{"oversize is clamped", 100000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				seen  []string
				token string
				pages int
			)
			for {
				resp, err := s.ListInventoryItems(customerCtx, &inventoryv1.ListInventoryItemsRequest{PageSize: tt.pageSize, PageToken: token})
				if err != nil {
					t.Fatal(err)
				}
				pages++
				for _, it := range resp.GetItems() {
					seen = append(seen, it.GetSku())
				}
				token = resp.GetNextPageToken()
				if token == "" {
					break
				}
				if pages > n+1 {
					t.Fatal("pagination did not terminate")
				}
			}
			if pages != tt.pages {
				t.Errorf("pages = %d, want %d", pages, tt.pages)
			}
			want := []string{"P-E", "P-D", "P-C", "P-B", "P-A"} // newest first
			if strings.Join(seen, ",") != strings.Join(want, ",") {
				t.Errorf("seen = %v, want %v", seen, want)
			}
		})
	}

	t.Run("invalid token", func(t *testing.T) {
		_, err := s.ListInventoryItems(customerCtx, &inventoryv1.ListInventoryItemsRequest{PageToken: "%%%not-a-token"})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("empty store returns empty page", func(t *testing.T) {
		empty, _ := newSvc(t)
		resp, err := empty.ListInventoryItems(customerCtx, &inventoryv1.ListInventoryItemsRequest{})
		if err != nil || len(resp.GetItems()) != 0 || resp.GetNextPageToken() != "" {
			t.Fatalf("resp = %v, err = %v", resp, err)
		}
	})
}

func TestStoreErrorsDoNotLeak(t *testing.T) {
	s, fs := newSvc(t)
	it := create(t, s, "LEAK-1", 1)
	fs.err = errors.New(`pq: relation "inventory_items" password=hunter2`)

	calls := map[string]func() error{
		"create": func() error {
			_, err := s.CreateInventoryItem(adminCtx, &inventoryv1.CreateInventoryItemRequest{Item: &inventoryv1.InventoryItem{Sku: "N-1"}})
			return err
		},
		"get": func() error {
			_, err := s.GetInventoryItem(customerCtx, &inventoryv1.GetInventoryItemRequest{Id: it.GetId()})
			return err
		},
		"update": func() error {
			_, err := s.UpdateInventoryItem(adminCtx, &inventoryv1.UpdateInventoryItemRequest{Item: &inventoryv1.InventoryItem{Id: it.GetId(), Sku: "N-2"}})
			return err
		},
		"delete": func() error {
			_, err := s.DeleteInventoryItem(adminCtx, &inventoryv1.DeleteInventoryItemRequest{Id: it.GetId()})
			return err
		},
		"list": func() error {
			_, err := s.ListInventoryItems(customerCtx, &inventoryv1.ListInventoryItemsRequest{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			wantCode(t, err, codes.Internal)
			if status.Convert(err).Message() != "internal error" {
				t.Errorf("message = %q, want generic", status.Convert(err).Message())
			}
		})
	}
}
