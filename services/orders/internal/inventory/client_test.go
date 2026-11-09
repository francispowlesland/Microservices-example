package inventory_test

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
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/inventory"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

type fakeServer struct {
	inventoryv1.UnimplementedInventoryServiceServer
	caller   authn.Claims
	released []*inventoryv1.StockLine
}

func (f *fakeServer) ReserveStock(ctx context.Context, req *inventoryv1.ReserveStockRequest) (*inventoryv1.ReserveStockResponse, error) {
	f.caller = authn.MustFromContext(ctx)
	if req.GetLines()[0].GetSku() == "SHORT" {
		return nil, status.Error(codes.FailedPrecondition, "SHORT: insufficient stock")
	}
	out := make([]*inventoryv1.StockLine, 0, len(req.GetLines()))
	for _, l := range req.GetLines() {
		out = append(out, &inventoryv1.StockLine{Sku: l.GetSku(), Quantity: l.GetQuantity(), UnitPriceCents: 99})
	}
	return &inventoryv1.ReserveStockResponse{Lines: out}, nil
}

func (f *fakeServer) ReleaseStock(ctx context.Context, req *inventoryv1.ReleaseStockRequest) (*inventoryv1.ReleaseStockResponse, error) {
	f.caller = authn.MustFromContext(ctx)
	f.released = req.GetLines()
	return &inventoryv1.ReleaseStockResponse{}, nil
}

// The client must authenticate with a token the real service-verifier accepts, and pass errors through.
func TestClientSignsServiceTokens(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeServer{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnaryInterceptor(authn.UnaryInterceptor(authn.NewServiceVerifier(pub), nil)))
	inventoryv1.RegisterInventoryServiceServer(srv, fs)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := inventory.New(conn, authn.NewServiceSigner(priv, time.Minute))
	ctx := context.Background()

	got, err := c.Reserve(ctx, []service.Line{{SKU: "A", Quantity: 2}, {SKU: "B", Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UnitPriceCents != 99 || got[1].SKU != "B" || got[0].Quantity != 2 {
		t.Fatalf("lines = %+v", got)
	}
	if !fs.caller.Service || fs.caller.Subject != "orders" {
		t.Errorf("caller = %+v, want service 'orders'", fs.caller)
	}

	if err := c.Release(ctx, []service.Line{{SKU: "A", Quantity: 2}}); err != nil || len(fs.released) != 1 || fs.released[0].GetQuantity() != 2 {
		t.Fatalf("release err=%v lines=%v", err, fs.released)
	}

	_, err = c.Reserve(ctx, []service.Line{{SKU: "SHORT", Quantity: 1}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("err = %v, want FailedPrecondition passed through", err)
	}

	// A client holding the wrong key is rejected by the verifier.
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	bad := inventory.New(conn, authn.NewServiceSigner(wrong, time.Minute))
	if _, err := bad.Reserve(ctx, []service.Line{{SKU: "A", Quantity: 1}}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("wrong key err = %v, want Unauthenticated", err)
	}
}
