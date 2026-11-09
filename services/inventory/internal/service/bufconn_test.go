package service_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/service"
)

// TestEndToEndThroughAuthInterceptor drives the real service through the real
// authn interceptor over an in-process bufconn listener.
func TestEndToEndThroughAuthInterceptor(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, time.Minute)
	svcPub, svcPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier := authn.Verifiers{authn.NewVerifier(pub, authn.Issuer, authn.Audience), authn.NewServiceVerifier(svcPub)}

	svcImpl, _ := newSvc(t)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(authn.UnaryInterceptor(verifier, nil)))
	inventoryv1.RegisterInventoryServiceServer(srv, svcImpl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := inventoryv1.NewInventoryServiceClient(conn)

	bearer := func(roles ...string) context.Context {
		tok, _, err := signer.Sign("00000000-0000-0000-0000-000000000001", roles)
		if err != nil {
			t.Fatal(err)
		}
		return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tok)
	}
	admin, customer := bearer("admin"), bearer("customer")
	anon := context.Background()

	svcTok, _, _ := authn.NewServiceSigner(svcPriv, time.Minute).Sign("orders", []string{"service"})
	svc := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+svcTok)

	created, err := client.CreateInventoryItem(admin, &inventoryv1.CreateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Sku: "e2e-1", Quantity: 7, PriceCents: 500},
	})
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	id := created.GetItem().GetId()
	if created.GetItem().GetSku() != "E2E-1" {
		t.Errorf("sku = %q, want E2E-1", created.GetItem().GetSku())
	}

	got, err := client.GetInventoryItem(customer, &inventoryv1.GetInventoryItemRequest{Id: id})
	if err != nil || got.GetItem().GetQuantity() != 7 {
		t.Fatalf("customer get = %v, %v", got, err)
	}
	list, err := client.ListInventoryItems(customer, &inventoryv1.ListInventoryItemsRequest{})
	if err != nil || len(list.GetItems()) != 1 {
		t.Fatalf("customer list = %v, %v", list, err)
	}

	writes := map[string]func(context.Context) error{
		"create": func(ctx context.Context) error {
			_, err := client.CreateInventoryItem(ctx, &inventoryv1.CreateInventoryItemRequest{Item: &inventoryv1.InventoryItem{Sku: "X-2", Quantity: 1}})
			return err
		},
		"update": func(ctx context.Context) error {
			_, err := client.UpdateInventoryItem(ctx, &inventoryv1.UpdateInventoryItemRequest{Item: &inventoryv1.InventoryItem{Id: id, Sku: "E2E-1", Quantity: 0}})
			return err
		},
		"delete": func(ctx context.Context) error {
			_, err := client.DeleteInventoryItem(ctx, &inventoryv1.DeleteInventoryItemRequest{Id: id})
			return err
		},
	}
	for name, call := range writes {
		if got := status.Code(call(customer)); got != codes.PermissionDenied {
			t.Errorf("customer %s = %v, want PermissionDenied", name, got)
		}
		if got := status.Code(call(anon)); got != codes.Unauthenticated {
			t.Errorf("anonymous %s = %v, want Unauthenticated", name, got)
		}
	}
	if _, err := client.GetInventoryItem(anon, &inventoryv1.GetInventoryItemRequest{Id: id}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("anonymous get = %v, want Unauthenticated", status.Code(err))
	}
	if _, err := client.ListInventoryItems(anon, &inventoryv1.ListInventoryItemsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("anonymous list = %v, want Unauthenticated", status.Code(err))
	}

	// Stock calls: only a service-signed token may reserve or release, even an admin user is refused.
	stockLines := []*inventoryv1.StockLine{{Sku: "E2E-1", Quantity: 3}}
	for _, who := range []struct {
		name string
		ctx  context.Context
		want codes.Code
	}{{"customer", customer, codes.PermissionDenied}, {"admin", admin, codes.PermissionDenied}, {"anon", anon, codes.Unauthenticated}} {
		_, err := client.ReserveStock(who.ctx, &inventoryv1.ReserveStockRequest{Lines: stockLines})
		if status.Code(err) != who.want {
			t.Errorf("%s reserve = %v, want %v", who.name, status.Code(err), who.want)
		}
	}
	res, err := client.ReserveStock(svc, &inventoryv1.ReserveStockRequest{Lines: stockLines})
	if err != nil || res.GetLines()[0].GetUnitPriceCents() != 500 {
		t.Fatalf("service reserve = %v, %v", res, err)
	}
	if _, err := client.ReleaseStock(svc, &inventoryv1.ReleaseStockRequest{Lines: stockLines}); err != nil {
		t.Fatalf("service release: %v", err)
	}
	if _, err := client.DeleteInventoryItem(svc, &inventoryv1.DeleteInventoryItemRequest{Id: id}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("service delete = %v, want PermissionDenied", status.Code(err))
	}

	// The denied customer writes must not have changed anything.
	got, err = client.GetInventoryItem(customer, &inventoryv1.GetInventoryItemRequest{Id: id})
	if err != nil || got.GetItem().GetQuantity() != 7 {
		t.Fatalf("item mutated by denied write: %v, %v", got, err)
	}
	if _, err := client.DeleteInventoryItem(admin, &inventoryv1.DeleteInventoryItemRequest{Id: id}); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
}

var _ service.Store = (*fakeStore)(nil)
