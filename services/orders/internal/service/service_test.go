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

func seed(t *testing.T, s *service.Server, ctx context.Context, total int64) string {
	t.Helper()
	resp, err := s.CreateOrder(ctx, &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{TotalCents: total}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return resp.GetOrder().GetId()
}

func TestNoClaimsIsUnauthenticated(t *testing.T) {
	s := service.New(newFakeStore(), nil)
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
		name     string
		ctx      context.Context
		in       *ordersv1.Order
		wantCode codes.Code
		wantUser uuid.UUID
	}{
		{"customer empty user defaults to self", aliceCtx(), &ordersv1.Order{TotalCents: 100}, codes.OK, alice},
		{"customer explicit self", aliceCtx(), &ordersv1.Order{UserId: alice.String(), TotalCents: 100}, codes.OK, alice},
		{"customer spoofs other user", aliceCtx(), &ordersv1.Order{UserId: bob.String(), TotalCents: 100}, codes.PermissionDenied, uuid.Nil},
		{"admin empty user defaults to self", adminCtx(), &ordersv1.Order{TotalCents: 100}, codes.OK, root},
		{"admin creates for other", adminCtx(), &ordersv1.Order{UserId: bob.String(), TotalCents: 100}, codes.OK, bob},
		{"zero total ok", aliceCtx(), &ordersv1.Order{TotalCents: 0}, codes.OK, alice},
		{"max total ok", aliceCtx(), &ordersv1.Order{TotalCents: service.MaxTotalCents}, codes.OK, alice},
		{"negative total", aliceCtx(), &ordersv1.Order{TotalCents: -1}, codes.InvalidArgument, uuid.Nil},
		{"oversized total", aliceCtx(), &ordersv1.Order{TotalCents: service.MaxTotalCents + 1}, codes.InvalidArgument, uuid.Nil},
		{"int64 max total", aliceCtx(), &ordersv1.Order{TotalCents: 1<<63 - 1}, codes.InvalidArgument, uuid.Nil},
		{"invalid user uuid", adminCtx(), &ordersv1.Order{UserId: "nope", TotalCents: 1}, codes.InvalidArgument, uuid.Nil},
		{"invalid user uuid as customer", aliceCtx(), &ordersv1.Order{UserId: "nope", TotalCents: 1}, codes.InvalidArgument, uuid.Nil},
		{"nil order", aliceCtx(), nil, codes.InvalidArgument, uuid.Nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := service.New(newFakeStore(), nil)
			resp, err := s.CreateOrder(tc.ctx, &ordersv1.CreateOrderRequest{Order: tc.in})
			wantCode(t, err, tc.wantCode)
			if tc.wantCode != codes.OK {
				return
			}
			if resp.GetOrder().GetUserId() != tc.wantUser.String() {
				t.Errorf("user_id = %s, want %s", resp.GetOrder().GetUserId(), tc.wantUser)
			}
			if resp.GetOrder().GetTotalCents() != tc.in.GetTotalCents() {
				t.Errorf("total = %d", resp.GetOrder().GetTotalCents())
			}
		})
	}
}

func TestCreateOrderIgnoresClientID(t *testing.T) {
	s := service.New(newFakeStore(), nil)
	client := uuid.New().String()
	resp, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{Id: client, TotalCents: 5}})
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
	s := service.New(newFakeStore(), nil)
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
	s := service.New(newFakeStore(), nil)
	id := seed(t, s, aliceCtx(), 10)

	tests := []struct {
		name string
		ctx  context.Context
		in   *ordersv1.Order
		want codes.Code
	}{
		{"owner denied", aliceCtx(), &ordersv1.Order{Id: id, UserId: alice.String(), TotalCents: 1}, codes.PermissionDenied},
		{"other denied", bobCtx(), &ordersv1.Order{Id: id, UserId: bob.String(), TotalCents: 1}, codes.PermissionDenied},
		{"denied before not-found", aliceCtx(), &ordersv1.Order{Id: uuid.NewString(), UserId: alice.String(), TotalCents: 1}, codes.PermissionDenied},
		{"admin reassigns", adminCtx(), &ordersv1.Order{Id: id, UserId: bob.String(), TotalCents: 99}, codes.OK},
		{"admin unknown id", adminCtx(), &ordersv1.Order{Id: uuid.NewString(), UserId: bob.String(), TotalCents: 1}, codes.NotFound},
		{"admin bad id", adminCtx(), &ordersv1.Order{Id: "x", UserId: bob.String(), TotalCents: 1}, codes.InvalidArgument},
		{"admin missing user", adminCtx(), &ordersv1.Order{Id: id, TotalCents: 1}, codes.InvalidArgument},
		{"admin bad user", adminCtx(), &ordersv1.Order{Id: id, UserId: "x", TotalCents: 1}, codes.InvalidArgument},
		{"admin negative", adminCtx(), &ordersv1.Order{Id: id, UserId: bob.String(), TotalCents: -5}, codes.InvalidArgument},
		{"admin oversized", adminCtx(), &ordersv1.Order{Id: id, UserId: bob.String(), TotalCents: service.MaxTotalCents + 1}, codes.InvalidArgument},
		{"admin nil order", adminCtx(), nil, codes.InvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.UpdateOrder(tc.ctx, &ordersv1.UpdateOrderRequest{Order: tc.in})
			wantCode(t, err, tc.want)
			if tc.want == codes.OK && (resp.GetOrder().GetUserId() != bob.String() || resp.GetOrder().GetTotalCents() != 99) {
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
			s := service.New(newFakeStore(), nil)
			owned := seed(t, s, aliceCtx(), 10)
			_, err := s.DeleteOrder(tc.ctx, &ordersv1.DeleteOrderRequest{Id: tc.id(owned)})
			wantCode(t, err, tc.want)
			_, getErr := s.GetOrder(adminCtx(), &ordersv1.GetOrderRequest{Id: owned})
			if tc.want == codes.OK {
				wantCode(t, getErr, codes.NotFound)
			} else if getErr != nil {
				t.Errorf("order should still exist: %v", getErr)
			}
		})
	}
}

func TestListOrdersFilterForcing(t *testing.T) {
	store := newFakeStore()
	s := service.New(store, nil)
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
			s := service.New(store, nil)
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
	s := service.New(newFakeStore(), nil)
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
	s := service.New(store, nil)
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
	s := service.New(store, nil)
	id := seed(t, s, aliceCtx(), 1)
	store.failWith = errBoom

	check := func(name string, err error) {
		t.Helper()
		wantCode(t, err, codes.Internal)
		if msg := status.Convert(err).Message(); msg != "internal error" || strings.Contains(msg, "10.0.0.1") {
			t.Errorf("%s: message %q leaks detail", name, msg)
		}
	}
	_, err := s.CreateOrder(aliceCtx(), &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{}})
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
	s := service.New(newFakeStore(), nil)
	ctx := authn.NewContext(context.Background(), authn.Claims{Subject: "not-a-uuid", Roles: []string{"customer"}})
	_, err := s.ListOrders(ctx, &ordersv1.ListOrdersRequest{})
	wantCode(t, err, codes.Unauthenticated)
}
