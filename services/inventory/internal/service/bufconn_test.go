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
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	svc, _ := newSvc(t)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(authn.UnaryInterceptor(verifier, nil)))
	inventoryv1.RegisterInventoryServiceServer(srv, svc)
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

	created, err := client.CreateInventoryItem(admin, &inventoryv1.CreateInventoryItemRequest{
		Item: &inventoryv1.InventoryItem{Sku: "e2e-1", Quantity: 7},
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
