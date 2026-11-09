package service_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	ordersv1 "github.com/francispowlesland/microservices-app-go/gen/go/orders/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

// TestBufconnAuthFlow runs the real gRPC server stack (generated registration and the real
// authn interceptor, no public methods) in process with tokens from authn.Signer.
func TestBufconnAuthFlow(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, time.Minute)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(authn.UnaryInterceptor(verifier, nil)))
	ordersv1.RegisterOrdersServiceServer(srv, service.New(newFakeStore(), nil))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := ordersv1.NewOrdersServiceClient(conn)

	withToken := func(id uuid.UUID, roles ...string) context.Context {
		tok, _, err := signer.Sign(id.String(), roles)
		if err != nil {
			t.Fatal(err)
		}
		return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tok)
	}
	customerA := withToken(alice, "customer")
	customerB := withToken(bob, "customer")
	admin := withToken(root, "admin")

	created, err := client.CreateOrder(customerA, &ordersv1.CreateOrderRequest{Order: &ordersv1.Order{TotalCents: 4200}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.GetOrder().GetId()
	if created.GetOrder().GetUserId() != alice.String() {
		t.Fatalf("user_id = %s", created.GetOrder().GetUserId())
	}

	if got, err := client.GetOrder(customerA, &ordersv1.GetOrderRequest{Id: id}); err != nil || got.GetOrder().GetTotalCents() != 4200 {
		t.Fatalf("owner get: %v %v", got, err)
	}
	_, err = client.GetOrder(customerB, &ordersv1.GetOrderRequest{Id: id})
	wantCode(t, err, codes.NotFound)
	if _, err := client.GetOrder(admin, &ordersv1.GetOrderRequest{Id: id}); err != nil {
		t.Fatalf("admin get: %v", err)
	}

	_, err = client.GetOrder(context.Background(), &ordersv1.GetOrderRequest{Id: id})
	wantCode(t, err, codes.Unauthenticated)
	_, err = client.ListOrders(context.Background(), &ordersv1.ListOrdersRequest{})
	wantCode(t, err, codes.Unauthenticated)

	bad := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer garbage")
	_, err = client.GetOrder(bad, &ordersv1.GetOrderRequest{Id: id})
	wantCode(t, err, codes.Unauthenticated)
}
