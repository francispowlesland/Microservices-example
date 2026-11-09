package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ordersv1 "github.com/francispowlesland/microservices-app-go/gen/go/orders/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

var (
	alice = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	bob   = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	root  = uuid.MustParse("99999999-9999-9999-9999-999999999999")
)

func ctxFor(id uuid.UUID, roles ...string) context.Context {
	return authn.NewContext(context.Background(), authn.Claims{Subject: id.String(), Roles: roles})
}

var (
	aliceCtx = func() context.Context { return ctxFor(alice, "customer") }
	bobCtx   = func() context.Context { return ctxFor(bob, "customer") }
	adminCtx = func() context.Context { return ctxFor(root, "admin") }
)

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

func oneWidget() []*ordersv1.OrderLine {
	return []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: 1}}
}

func seed(t *testing.T, s *service.Server, ctx context.Context, _ int64) string {
	t.Helper()
	resp, err := s.CreateOrder(ctx, &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Lines: oneWidget()}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return resp.GetOrder().GetId()
}

func TestNoClaimsIsUnauthenticated(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	ctx := context.Background()
	_, err := s.CreateOrder(ctx, &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{}})
	wantCode(t, err, codes.Unauthenticated)
	_, err = s.GetOrder(ctx, &ordersv1.GetOrderRequest{Id: alice.String()})
	wantCode(t, err, codes.Unauthenticated)
	_, err = s.ListOrders(ctx, &ordersv1.ListOrdersRequest{})
	wantCode(t, err, codes.Unauthenticated)
}

func TestCreateOrder(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		in        *ordersv1.Order
		wantCode  codes.Code
		wantUser  uuid.UUID
		wantTotal int64
	}{
		{"customer empty user defaults to self", aliceCtx(), &ordersv1.Order{Lines: oneWidget()}, codes.OK, alice, 1000},
		{"customer explicit self", aliceCtx(), &ordersv1.Order{UserId: alice.String(), Lines: oneWidget()}, codes.OK, alice, 1000},
		{"customer spoofs other user", aliceCtx(), &ordersv1.Order{UserId: bob.String(), Lines: oneWidget()}, codes.PermissionDenied, uuid.Nil, 0},
		{"admin empty user defaults to self", adminCtx(), &ordersv1.Order{Lines: oneWidget()}, codes.OK, root, 1000},
		{"admin creates for other", adminCtx(), &ordersv1.Order{UserId: bob.String(), Lines: oneWidget()}, codes.OK, bob, 1000},
		{"several lines are summed", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: 2}, {Sku: "GADGET", Quantity: 3}}}, codes.OK, alice, 2750},
		{"client total is ignored", aliceCtx(), &ordersv1.Order{TotalCents: 1, Lines: oneWidget()}, codes.OK, alice, 1000},
		{"client unit price is ignored", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: 1, UnitPriceCents: 1}}}, codes.OK, alice, 1000},
		{"no lines", aliceCtx(), &ordersv1.Order{}, codes.InvalidArgument, uuid.Nil, 0},
		{"too many lines", aliceCtx(), &ordersv1.Order{Lines: make([]*ordersv1.OrderLine, service.MaxLines+1)}, codes.InvalidArgument, uuid.Nil, 0},
		{"blank sku", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: " ", Quantity: 1}}}, codes.InvalidArgument, uuid.Nil, 0},
		{"zero quantity", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "WIDGET"}}}, codes.InvalidArgument, uuid.Nil, 0},
		{"negative quantity", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: -1}}}, codes.InvalidArgument, uuid.Nil, 0},
		{"unknown sku", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "NOPE", Quantity: 1}}}, codes.InvalidArgument, uuid.Nil, 0},
		{"insufficient stock", aliceCtx(), &ordersv1.Order{Lines: []*ordersv1.OrderLine{{Sku: "GADGET", Quantity: 6}}}, codes.FailedPrecondition, uuid.Nil, 0},
		{"invalid user uuid", adminCtx(), &ordersv1.Order{UserId: "nope", Lines: oneWidget()}, codes.InvalidArgument, uuid.Nil, 0},
		{"invalid user uuid as customer", aliceCtx(), &ordersv1.Order{UserId: "nope", Lines: oneWidget()}, codes.InvalidArgument, uuid.Nil, 0},
		{"nil order", aliceCtx(), nil, codes.InvalidArgument, uuid.Nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv := newFakeInv()
			s := service.New(newFakeStore(), inv, nil)
			resp, err := s.CreateOrder(tc.ctx, &ordersv1.CreateOrderRequest{Order: tc.in})
			wantCode(t, err, tc.wantCode)
			if tc.wantCode != codes.OK {
				if inv.stock["WIDGET"] != 1000 || inv.stock["GADGET"] != 5 {
					t.Errorf("stock changed by a rejected order: %v", inv.stock)
				}
				return
			}
			if resp.GetOrder().GetUserId() != tc.wantUser.String() {
				t.Errorf("user_id = %s, want %s", resp.GetOrder().GetUserId(), tc.wantUser)
			}
			if resp.GetOrder().GetTotalCents() != tc.wantTotal {
				t.Errorf("total = %d, want %d", resp.GetOrder().GetTotalCents(), tc.wantTotal)
			}
			if len(resp.GetOrder().GetLines()) != len(tc.in.GetLines()) {
				t.Errorf("lines = %v", resp.GetOrder().GetLines())
			}
		})
	}
}

func TestCreateOrderReservesStock(t *testing.T) {
	inv := newFakeInv()
	s := service.New(newFakeStore(), inv, nil)
	_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{
		Lines: []*ordersv1.OrderLine{{Sku: "GADGET", Quantity: 5}}}})
	if err != nil || inv.stock["GADGET"] != 0 {
		t.Fatalf("err=%v stock=%d, want 0", err, inv.stock["GADGET"])
	}
	_, err = s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{
		Lines: []*ordersv1.OrderLine{{Sku: "GADGET", Quantity: 1}}}})
	wantCode(t, err, codes.FailedPrecondition)
}

func TestCreateOrderGivesStockBackWhenSaveFails(t *testing.T) {
	inv := newFakeInv()
	store := newFakeStore()
	store.failWith = errBoom
	s := service.New(store, inv, nil)
	_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Lines: oneWidget()}})
	wantCode(t, err, codes.Internal)
	if inv.stock["WIDGET"] != 1000 || len(inv.released) != 1 {
		t.Errorf("stock=%d releases=%d, want 1000 and 1", inv.stock["WIDGET"], len(inv.released))
	}
}

func TestCreateOrderTotalTooLargeGivesStockBack(t *testing.T) {
	inv := newFakeInv()
	inv.price["WIDGET"] = 1_000_000_000
	inv.stock["WIDGET"] = 5000
	s := service.New(newFakeStore(), inv, nil)
	_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{
		Lines: []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: 1001}}}})
	wantCode(t, err, codes.InvalidArgument)
	if inv.stock["WIDGET"] != 5000 || len(inv.released) != 1 {
		t.Errorf("stock=%d releases=%d, want 5000 and 1", inv.stock["WIDGET"], len(inv.released))
	}
}

func TestCreateOrderInventoryOutage(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want codes.Code
	}{
		"unavailable": {status.Error(codes.Unavailable, "dial tcp 10.0.0.9: refused"), codes.Unavailable},
		"timeout":     {status.Error(codes.DeadlineExceeded, "ctx"), codes.Unavailable},
		"denied":      {status.Error(codes.PermissionDenied, "service identity required"), codes.Internal},
		"other":       {errBoom, codes.Internal},
	} {
		t.Run(name, func(t *testing.T) {
			inv := newFakeInv()
			inv.failWith = tc.err
			s := service.New(newFakeStore(), inv, nil)
			_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Lines: oneWidget()}})
			wantCode(t, err, tc.want)
			if msg := status.Convert(err).Message(); strings.Contains(msg, "10.0.0.") {
				t.Errorf("message %q leaks detail", msg)
			}
		})
	}
}

func TestCreateOrderIgnoresClientID(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	client := uuid.New().String()
	resp, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Id: client, Lines: oneWidget()}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOrder().GetId() == client {
		t.Fatal("client-supplied id was used")
	}
	if _, err := uuid.Parse(resp.GetOrder().GetId()); err != nil {
		t.Fatalf("generated id not a uuid: %v", err)
	}
}

func TestGetOrder(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	id := seed(t, s, aliceCtx(), 10)

	tests := []struct {
		name string
		ctx  context.Context
		id   string
		want codes.Code
	}{
		{"owner", aliceCtx(), id, codes.OK},
		{"other user gets NotFound", bobCtx(), id, codes.NotFound},
		{"admin", adminCtx(), id, codes.OK},
		{"unknown id", aliceCtx(), uuid.NewString(), codes.NotFound},
		{"unknown id admin", adminCtx(), uuid.NewString(), codes.NotFound},
		{"invalid uuid", aliceCtx(), "x", codes.InvalidArgument},
		{"empty id", aliceCtx(), "", codes.InvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.GetOrder(tc.ctx, &ordersv1.GetOrderRequest{Id: tc.id})
			wantCode(t, err, tc.want)
			if tc.want == codes.OK && resp.GetOrder().GetId() != id {
				t.Errorf("id = %s", resp.GetOrder().GetId())
			}
		})
	}
}

func TestUpdateOrder(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	id := seed(t, s, aliceCtx(), 0)

	tests := []struct {
		name string
		ctx  context.Context
		in   *ordersv1.Order
		want codes.Code
	}{
		{"owner denied", aliceCtx(), &ordersv1.Order{Id: id, UserId: alice.String()}, codes.PermissionDenied},
		{"other denied", bobCtx(), &ordersv1.Order{Id: id, UserId: bob.String()}, codes.PermissionDenied},
		{"denied before not-found", aliceCtx(), &ordersv1.Order{Id: uuid.NewString(), UserId: alice.String()}, codes.PermissionDenied},
		{"admin reassigns", adminCtx(), &ordersv1.Order{Id: id, UserId: bob.String()}, codes.OK},
		{"admin unknown id", adminCtx(), &ordersv1.Order{Id: uuid.NewString(), UserId: bob.String()}, codes.NotFound},
		{"admin bad id", adminCtx(), &ordersv1.Order{Id: "x", UserId: bob.String()}, codes.InvalidArgument},
		{"admin missing user", adminCtx(), &ordersv1.Order{Id: id}, codes.InvalidArgument},
		{"admin bad user", adminCtx(), &ordersv1.Order{Id: id, UserId: "x"}, codes.InvalidArgument},
		{"admin nil order", adminCtx(), nil, codes.InvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.UpdateOrder(tc.ctx, &ordersv1.UpdateOrderRequest{Order: tc.in})
			wantCode(t, err, tc.want)
			if tc.want == codes.OK && resp.GetOrder().GetUserId() != bob.String() {
				t.Errorf("unexpected response %v", resp.GetOrder())
			}
		})
	}

	// The reassigned order is now bob's, not alice's.
	if _, err := s.GetOrder(aliceCtx(), &ordersv1.GetOrderRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Errorf("alice should no longer see the order, got %v", err)
	}
	if _, err := s.GetOrder(bobCtx(), &ordersv1.GetOrderRequest{Id: id}); err != nil {
		t.Errorf("bob should see the order: %v", err)
	}
}

func TestUpdateOrderCannotChangeTotalOrLines(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	id := seed(t, s, aliceCtx(), 0)
	resp, err := s.UpdateOrder(adminCtx(), &ordersv1.UpdateOrderRequest{Order: &ordersv1.Order{
		Id: id, UserId: alice.String(), TotalCents: 1,
		Lines: []*ordersv1.OrderLine{{Sku: "GADGET", Quantity: 4}}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOrder().GetTotalCents() != 1000 || len(resp.GetOrder().GetLines()) != 1 || resp.GetOrder().GetLines()[0].GetSku() != "WIDGET" {
		t.Errorf("order changed: %v", resp.GetOrder())
	}
}

func TestDeleteOrder(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		id   func(owned string) string
		want codes.Code
	}{
		{"owner denied", aliceCtx(), func(o string) string { return o }, codes.PermissionDenied},
		{"other denied", bobCtx(), func(o string) string { return o }, codes.PermissionDenied},
		{"admin ok", adminCtx(), func(o string) string { return o }, codes.OK},
		{"admin unknown", adminCtx(), func(string) string { return uuid.NewString() }, codes.NotFound},
		{"admin bad id", adminCtx(), func(string) string { return "x" }, codes.InvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv := newFakeInv()
			s := service.New(newFakeStore(), inv, nil)
			owned := seed(t, s, aliceCtx(), 0)
			_, err := s.DeleteOrder(tc.ctx, &ordersv1.DeleteOrderRequest{Id: tc.id(owned)})
			wantCode(t, err, tc.want)
			_, getErr := s.GetOrder(adminCtx(), &ordersv1.GetOrderRequest{Id: owned})
			wantStock := int32(999) // the seeded order still holds one widget
			if tc.want == codes.OK {
				wantCode(t, getErr, codes.NotFound)
				wantStock = 1000
			} else if getErr != nil {
				t.Errorf("order should still exist: %v", getErr)
			}
			if inv.stock["WIDGET"] != wantStock {
				t.Errorf("stock = %d, want %d", inv.stock["WIDGET"], wantStock)
			}
		})
	}
}

func TestDeleteOrderReleasesEveryLine(t *testing.T) {
	inv := newFakeInv()
	s := service.New(newFakeStore(), inv, nil)
	resp, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{
		Lines: []*ordersv1.OrderLine{{Sku: "WIDGET", Quantity: 3}, {Sku: "GADGET", Quantity: 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if inv.stock["WIDGET"] != 997 || inv.stock["GADGET"] != 3 {
		t.Fatalf("after create stock = %v", inv.stock)
	}
	if _, err := s.DeleteOrder(adminCtx(), &ordersv1.DeleteOrderRequest{Id: resp.GetOrder().GetId()}); err != nil {
		t.Fatal(err)
	}
	if inv.stock["WIDGET"] != 1000 || inv.stock["GADGET"] != 5 {
		t.Errorf("after delete stock = %v, want 1000/5", inv.stock)
	}
}

// If the stock service is down the order is still deleted: the failed release is logged for manual correction.
func TestDeleteOrderSucceedsWhenReleaseFails(t *testing.T) {
	inv := newFakeInv()
	s := service.New(newFakeStore(), inv, nil)
	id := seed(t, s, aliceCtx(), 0)
	inv.relErr = status.Error(codes.Unavailable, "down")
	if _, err := s.DeleteOrder(adminCtx(), &ordersv1.DeleteOrderRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetOrder(adminCtx(), &ordersv1.GetOrderRequest{Id: id})
	wantCode(t, err, codes.NotFound)
}

func TestListOrdersFilterForcing(t *testing.T) {
	store := newFakeStore()
	s := service.New(store, newFakeInv(), nil)
	seed(t, s, aliceCtx(), 1)
	seed(t, s, aliceCtx(), 2)
	seed(t, s, bobCtx(), 3)

	tests := []struct {
		name     string
		ctx      context.Context
		userID   string
		wantCode codes.Code
		wantLen  int
	}{
		{"customer no filter sees only own", aliceCtx(), "", codes.OK, 2},
		{"customer own filter", aliceCtx(), alice.String(), codes.OK, 2},
		{"customer other filter denied", aliceCtx(), bob.String(), codes.PermissionDenied, 0},
		{"customer invalid filter", aliceCtx(), "x", codes.InvalidArgument, 0},
		{"admin no filter sees all", adminCtx(), "", codes.OK, 3},
		{"admin filters alice", adminCtx(), alice.String(), codes.OK, 2},
		{"admin filters bob", adminCtx(), bob.String(), codes.OK, 1},
		{"admin invalid filter", adminCtx(), "x", codes.InvalidArgument, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.ListOrders(tc.ctx, &ordersv1.ListOrdersRequest{UserId: tc.userID})
			wantCode(t, err, tc.wantCode)
			if tc.wantCode == codes.OK && len(resp.GetOrders()) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(resp.GetOrders()), tc.wantLen)
			}
		})
	}

	// Non-admin store query must be forced to the caller even with no filter.
	if _, err := s.ListOrders(bobCtx(), &ordersv1.ListOrdersRequest{}); err != nil {
		t.Fatal(err)
	}
	if store.lastList.UserID == nil || *store.lastList.UserID != bob {
		t.Errorf("store filter = %v, want bob", store.lastList.UserID)
	}
}

func TestListOrdersPagination(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprintf("sameCreatedAt=%v", same), func(t *testing.T) {
			store := newFakeStore()
			store.sameTime = same
			s := service.New(store, newFakeInv(), nil)
			for i := 0; i < 7; i++ {
				seed(t, s, aliceCtx(), int64(i))
			}
			seed(t, s, bobCtx(), 1000)

			seen := map[string]bool{}
			token, pages := "", 0
			for {
				resp, err := s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{PageSize: 3, PageToken: token})
				if err != nil {
					t.Fatal(err)
				}
				pages++
				for _, o := range resp.GetOrders() {
					if seen[o.GetId()] {
						t.Fatalf("duplicate %s", o.GetId())
					}
					seen[o.GetId()] = true
					if o.GetUserId() != alice.String() {
						t.Fatalf("leaked order of %s", o.GetUserId())
					}
				}
				if resp.GetNextPageToken() == "" {
					break
				}
				token = resp.GetNextPageToken()
			}
			if len(seen) != 7 || pages != 3 {
				t.Errorf("seen=%d pages=%d, want 7 and 3", len(seen), pages)
			}
		})
	}
}

func TestListOrdersFilterNotTakenFromToken(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	for i := 0; i < 4; i++ {
		seed(t, s, bobCtx(), 1)
		seed(t, s, aliceCtx(), 1)
	}
	// Bob obtains a token, then Alice replays it: she must still only see her own rows.
	page, err := s.ListOrders(bobCtx(), &ordersv1.ListOrdersRequest{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{PageToken: page.GetNextPageToken()})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range resp.GetOrders() {
		if o.GetUserId() != alice.String() {
			t.Fatalf("leaked %s", o.GetUserId())
		}
	}
}

func TestListOrdersPageSizeAndToken(t *testing.T) {
	store := newFakeStore()
	s := service.New(store, newFakeInv(), nil)
	_, err := s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{PageToken: "!!!not-a-token"})
	wantCode(t, err, codes.InvalidArgument)

	if _, err := s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{PageSize: 100000}); err != nil {
		t.Fatal(err)
	}
	if store.lastList.Limit != 201 {
		t.Errorf("limit = %d, want 201 (max 200 + 1)", store.lastList.Limit)
	}
	if _, err := s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{}); err != nil {
		t.Fatal(err)
	}
	if store.lastList.Limit != 51 {
		t.Errorf("limit = %d, want 51", store.lastList.Limit)
	}
}

func TestStoreErrorsAreNotLeaked(t *testing.T) {
	store := newFakeStore()
	s := service.New(store, newFakeInv(), nil)
	id := seed(t, s, aliceCtx(), 1)
	store.failWith = errBoom

	check := func(name string, err error) {
		t.Helper()
		wantCode(t, err, codes.Internal)
		if msg := status.Convert(err).Message(); msg != "internal error" || strings.Contains(msg, "10.0.0.1") {
			t.Errorf("%s: message %q leaks detail", name, msg)
		}
	}
	_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Lines: oneWidget()}})
	check("create", err)
	_, err = s.GetOrder(aliceCtx(), &ordersv1.GetOrderRequest{Id: id})
	check("get", err)
	_, err = s.UpdateOrder(adminCtx(), &ordersv1.UpdateOrderRequest{Order: &ordersv1.Order{Id: id, UserId: alice.String()}})
	check("update", err)
	_, err = s.DeleteOrder(adminCtx(), &ordersv1.DeleteOrderRequest{Id: id})
	check("delete", err)
	_, err = s.ListOrders(aliceCtx(), &ordersv1.ListOrdersRequest{})
	check("list", err)
}

func TestInvalidSubjectIsUnauthenticated(t *testing.T) {
	s := service.New(newFakeStore(), newFakeInv(), nil)
	ctx := authn.NewContext(context.Background(), authn.Claims{Subject: "not-a-uuid", Roles: []string{"customer"}})
	_, err := s.ListOrders(ctx, &ordersv1.ListOrdersRequest{})
	wantCode(t, err, codes.Unauthenticated)
}
