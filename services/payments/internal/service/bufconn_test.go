package service

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
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

// TestEndToEndOverBufconn drives the service through a real gRPC server with the
// production auth interceptor and real signed JWTs.
func TestEndToEndOverBufconn(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, time.Hour)
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnaryInterceptor(authn.UnaryInterceptor(verifier, nil)))
	paymentsv1.RegisterPaymentsServiceServer(srv, New(newFakeStore(), maxCents, nil))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := paymentsv1.NewPaymentsServiceClient(conn)

	as := func(id uuid.UUID, roles ...string) context.Context {
		tok, _, err := signer.Sign(id.String(), roles)
		if err != nil {
			t.Fatal(err)
		}
		return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tok)
	}
	customer, other, root := as(uuid.New(), "customer"), as(uuid.New(), "customer"), as(uuid.New(), "admin")

	// No token: Unauthenticated.
	_, err = client.ListPayments(context.Background(), &paymentsv1.ListPaymentsRequest{})
	wantCode(t, err, codes.Unauthenticated)

	req := payReq(uuid.NewString(), 250, "GBP", "order-1-attempt")
	first, err := client.ProcessPayment(customer, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetPayment().GetStatus() != paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
		t.Fatalf("status = %v", first.GetPayment().GetStatus())
	}
	id := first.GetPayment().GetId()

	// Retry with the same key: same payment.
	retry, err := client.ProcessPayment(customer, req)
	if err != nil || retry.GetPayment().GetId() != id {
		t.Fatalf("retry must return the original: %v %v", retry, err)
	}

	// Owner reads; another customer cannot.
	if _, err := client.GetPayment(customer, &paymentsv1.GetPaymentRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	_, err = client.GetPayment(other, &paymentsv1.GetPaymentRequest{Id: id})
	wantCode(t, err, codes.NotFound)

	// Customers cannot refund; admin can; a second refund fails.
	_, err = client.RefundPayment(customer, &paymentsv1.RefundPaymentRequest{Id: id})
	wantCode(t, err, codes.PermissionDenied)
	refund, err := client.RefundPayment(root, &paymentsv1.RefundPaymentRequest{Id: id, Reason: "customer request"})
	if err != nil || refund.GetPayment().GetStatus() != paymentsv1.PaymentStatus_PAYMENT_STATUS_REFUNDED {
		t.Fatalf("admin refund: %v %v", refund, err)
	}
	_, err = client.RefundPayment(root, &paymentsv1.RefundPaymentRequest{Id: id})
	wantCode(t, err, codes.FailedPrecondition)

	// A declined payment is a normal response, not an error.
	declined, err := client.ProcessPayment(customer, payReq(uuid.NewString(), maxCents+1, "USD", "big"))
	if err != nil || declined.GetPayment().GetStatus() != paymentsv1.PaymentStatus_PAYMENT_STATUS_FAILED {
		t.Fatalf("declined: %v %v", declined, err)
	}
	if got := status.Code(err); got != codes.OK {
		t.Fatalf("code = %v", got)
	}
}
