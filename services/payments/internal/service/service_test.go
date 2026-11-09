package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

const maxCents = 1000

var (
	adminID = uuid.New()
	aliceID = uuid.New()
	bobID   = uuid.New()
)

func ctxFor(id uuid.UUID, roles ...string) context.Context {
	return authn.NewContext(context.Background(), authn.Claims{Subject: id.String(), Roles: roles})
}

func admin() context.Context { return ctxFor(adminID, "admin") }
func alice() context.Context { return ctxFor(aliceID, "customer") }
func bob() context.Context   { return ctxFor(bobID, "customer") }

func newServer(t *testing.T) (*Server, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	return New(fs, maxCents, nil), fs
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

func payReq(order string, amount int64, cur, key string) *paymentsv1.ProcessPaymentRequest {
	return &paymentsv1.ProcessPaymentRequest{OrderId: order, AmountCents: amount, Currency: cur, IdempotencyKey: key}
}

func TestRegistration(t *testing.T) {
	s := grpc.NewServer()
	paymentsv1.RegisterPaymentsServiceServer(s, New(newFakeStore(), maxCents, nil))
	info := s.GetServiceInfo()["payments.v1.PaymentsService"]
	got := map[string]bool{}
	for _, m := range info.Methods {
		if m.IsClientStream || m.IsServerStream {
			t.Errorf("%s must be unary", m.Name)
		}
		got[m.Name] = true
	}
	for _, m := range []string{"ProcessPayment", "GetPayment", "ListPayments", "RefundPayment"} {
		if !got[m] {
			t.Errorf("method %s not registered", m)
		}
	}
	// Every RPC must be overridden, not served by the Unimplemented fallback.
	s2, _ := newServer(t)
	_, err := s2.ProcessPayment(alice(), payReq("bad", 1, "USD", "k"))
	wantCode(t, err, codes.InvalidArgument)
}

func TestProcessPaymentValidation(t *testing.T) {
	order := uuid.NewString()
	tests := []struct {
		name string
		req  *paymentsv1.ProcessPaymentRequest
	}{
		{"bad order uuid", payReq("nope", 100, "USD", "k")},
		{"empty order", payReq("", 100, "USD", "k")},
		{"zero amount", payReq(order, 0, "USD", "k")},
		{"negative amount", payReq(order, -5, "USD", "k")},
		{"lower-case currency", payReq(order, 100, "usd", "k")},
		{"unsupported currency", payReq(order, 100, "JPY", "k")},
		{"empty currency", payReq(order, 100, "", "k")},
		{"missing key", payReq(order, 100, "USD", "")},
		{"oversized key", payReq(order, 100, "USD", strings.Repeat("a", 129))},
		{"non-printable key", payReq(order, 100, "USD", "a\tb")},
		{"non-ascii key", payReq(order, 100, "USD", "caf\u00e9")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, fs := newServer(t)
			_, err := s.ProcessPayment(alice(), tc.req)
			wantCode(t, err, codes.InvalidArgument)
			if len(fs.rows) != 0 {
				t.Fatal("invalid request must not write")
			}
		})
	}
	t.Run("max length key ok", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.ProcessPayment(alice(), payReq(order, 100, "USD", strings.Repeat("a", 128)))
		wantCode(t, err, codes.OK)
	})
}

func TestProcessPaymentOutcome(t *testing.T) {
	order := uuid.NewString()
	tests := []struct {
		name       string
		amount     int64
		wantStatus paymentsv1.PaymentStatus
		wantReason string
	}{
		{"below limit", maxCents - 1, paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED, ""},
		{"at limit", maxCents, paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED, ""},
		{"over limit is FAILED not an error", maxCents + 1, paymentsv1.PaymentStatus_PAYMENT_STATUS_FAILED, "amount exceeds limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newServer(t)
			resp, err := s.ProcessPayment(alice(), payReq(order, tc.amount, "EUR", "k1"))
			if err != nil {
				t.Fatal(err)
			}
			p := resp.GetPayment()
			if p.GetStatus() != tc.wantStatus || p.GetFailureReason() != tc.wantReason {
				t.Fatalf("got %v %q", p.GetStatus(), p.GetFailureReason())
			}
			if p.GetOrderId() != order || p.GetAmountCents() != tc.amount || p.GetCurrency() != "EUR" {
				t.Fatalf("payment fields wrong: %v", p)
			}
			if _, err := uuid.Parse(p.GetId()); err != nil {
				t.Fatalf("id not a UUID: %v", err)
			}
		})
	}
}

func TestProcessPaymentIdempotency(t *testing.T) {
	order := uuid.NewString()
	s, fs := newServer(t)
	first, err := s.ProcessPayment(alice(), payReq(order, 100, "USD", "k"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ProcessPayment(alice(), payReq(order, 100, "USD", "k"))
	if err != nil {
		t.Fatal(err)
	}
	if first.GetPayment().GetId() != again.GetPayment().GetId() || len(fs.rows) != 1 {
		t.Fatalf("replay must return original: %v vs %v, rows=%d", first, again, len(fs.rows))
	}

	for name, req := range map[string]*paymentsv1.ProcessPaymentRequest{
		"different order":    payReq(uuid.NewString(), 100, "USD", "k"),
		"different amount":   payReq(order, 101, "USD", "k"),
		"different currency": payReq(order, 100, "EUR", "k"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.ProcessPayment(alice(), req)
			wantCode(t, err, codes.InvalidArgument)
			if !strings.Contains(status.Convert(err).Message(), "idempotency key reused with different parameters") {
				t.Fatalf("message = %q", status.Convert(err).Message())
			}
		})
	}

	// The key is scoped to the caller: bob may use the same key.
	other, err := s.ProcessPayment(bob(), payReq(order, 100, "USD", "k"))
	if err != nil || other.GetPayment().GetId() == first.GetPayment().GetId() {
		t.Fatalf("key must be per caller: %v %v", other, err)
	}
}

func TestAuthMatrix(t *testing.T) {
	s, _ := newServer(t)
	order := uuid.NewString()
	resp, err := s.ProcessPayment(alice(), payReq(order, 100, "USD", "k"))
	if err != nil {
		t.Fatal(err)
	}
	id := resp.GetPayment().GetId()

	t.Run("get", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			ctx  context.Context
			want codes.Code
		}{
			{"owner", alice(), codes.OK},
			{"admin", admin(), codes.OK},
			{"other customer sees NotFound", bob(), codes.NotFound},
		} {
			_, err := s.GetPayment(tc.ctx, &paymentsv1.GetPaymentRequest{Id: id})
			if got := status.Code(err); got != tc.want {
				t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
			}
		}
		_, err := s.GetPayment(alice(), &paymentsv1.GetPaymentRequest{Id: uuid.NewString()})
		wantCode(t, err, codes.NotFound)
		_, err = s.GetPayment(alice(), &paymentsv1.GetPaymentRequest{Id: "x"})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("list", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			ctx  context.Context
			want int
		}{
			{"owner", alice(), 1},
			{"admin", admin(), 1},
			{"other customer", bob(), 0},
		} {
			r, err := s.ListPayments(tc.ctx, &paymentsv1.ListPaymentsRequest{})
			if err != nil || len(r.GetPayments()) != tc.want {
				t.Errorf("%s: got %d, %v; want %d", tc.name, len(r.GetPayments()), err, tc.want)
			}
		}
		_, err := s.ListPayments(alice(), &paymentsv1.ListPaymentsRequest{OrderId: "x"})
		wantCode(t, err, codes.InvalidArgument)
		_, err = s.ListPayments(alice(), &paymentsv1.ListPaymentsRequest{PageToken: "!!"})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("refund", func(t *testing.T) {
		_, err := s.RefundPayment(alice(), &paymentsv1.RefundPaymentRequest{Id: id})
		wantCode(t, err, codes.PermissionDenied)
		_, err = s.RefundPayment(bob(), &paymentsv1.RefundPaymentRequest{Id: id})
		wantCode(t, err, codes.PermissionDenied)
		_, err = s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: id})
		wantCode(t, err, codes.OK)
	})

	t.Run("bad subject", func(t *testing.T) {
		ctx := authn.NewContext(context.Background(), authn.Claims{Subject: "not-a-uuid"})
		_, err := s.ProcessPayment(ctx, payReq(order, 1, "USD", "z"))
		wantCode(t, err, codes.Unauthenticated)
	})
}

func TestListFiltersAndPagination(t *testing.T) {
	s, _ := newServer(t)
	orderA, orderB := uuid.NewString(), uuid.NewString()
	if _, err := s.ProcessPayment(bob(), payReq(orderA, 10, "USD", "b1")); err != nil {
		t.Fatal(err)
	}
	for i, o := range []string{orderA, orderA, orderB, orderA, orderB} {
		if _, err := s.ProcessPayment(alice(), payReq(o, 10, "USD", string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}

	collect := func(ctx context.Context, order string, size int32) (ids []string, pages int) {
		token := ""
		for {
			r, err := s.ListPayments(ctx, &paymentsv1.ListPaymentsRequest{OrderId: order, PageSize: size, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			pages++
			for _, p := range r.GetPayments() {
				ids = append(ids, p.GetId())
			}
			if r.GetNextPageToken() == "" {
				return
			}
			token = r.GetNextPageToken()
		}
	}

	ids, pages := collect(alice(), "", 2)
	if len(ids) != 5 || pages != 3 {
		t.Fatalf("alice all: %d ids over %d pages", len(ids), pages)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatal("duplicate across pages")
		}
		seen[id] = true
	}
	if ids, _ := collect(alice(), orderA, 2); len(ids) != 3 {
		t.Fatalf("alice orderA: %d", len(ids))
	}
	if ids, _ := collect(admin(), orderA, 2); len(ids) != 4 {
		t.Fatalf("admin orderA: %d", len(ids))
	}
	if ids, _ := collect(admin(), "", 200); len(ids) != 6 {
		t.Fatalf("admin all: %d", len(ids))
	}
	// A token minted for alice's listing grants bob nothing: filters come from claims.
	r, _ := s.ListPayments(alice(), &paymentsv1.ListPaymentsRequest{PageSize: 1})
	r2, err := s.ListPayments(bob(), &paymentsv1.ListPaymentsRequest{PageToken: r.GetNextPageToken()})
	if err != nil || len(r2.GetPayments()) != 1 {
		t.Fatalf("bob must only see his own: %v %v", r2, err)
	}
}

func TestRefundStateMachine(t *testing.T) {
	order := uuid.NewString()
	tests := []struct {
		name   string
		amount int64
		prep   func(t *testing.T, s *Server, id string)
		want   codes.Code
	}{
		{"SUCCEEDED -> REFUNDED", 10, nil, codes.OK},
		{"FAILED cannot be refunded", maxCents + 1, nil, codes.FailedPrecondition},
		{"REFUNDED cannot be refunded again", 10, func(t *testing.T, s *Server, id string) {
			if _, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: id}); err != nil {
				t.Fatal(err)
			}
		}, codes.FailedPrecondition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, fs := newServer(t)
			r, err := s.ProcessPayment(alice(), payReq(order, tc.amount, "USD", "k"))
			if err != nil {
				t.Fatal(err)
			}
			id := r.GetPayment().GetId()
			if tc.prep != nil {
				tc.prep(t, s, id)
			}
			resp, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: id, Reason: "  damaged  "})
			wantCode(t, err, tc.want)
			if tc.want == codes.OK {
				if resp.GetPayment().GetStatus() != paymentsv1.PaymentStatus_PAYMENT_STATUS_REFUNDED {
					t.Fatalf("status = %v", resp.GetPayment().GetStatus())
				}
				if got := fs.rows[uuid.MustParse(id)].RefundReason; got != "damaged" {
					t.Fatalf("reason not trimmed: %q", got)
				}
			}
		})
	}

	t.Run("unknown id", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: uuid.NewString()})
		wantCode(t, err, codes.NotFound)
	})
	t.Run("bad id", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: "x"})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("reason too long", func(t *testing.T) {
		s, _ := newServer(t)
		_, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: uuid.NewString(), Reason: strings.Repeat("x", 501)})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("reason at limit", func(t *testing.T) {
		s, _ := newServer(t)
		r, _ := s.ProcessPayment(alice(), payReq(order, 10, "USD", "k"))
		_, err := s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: r.GetPayment().GetId(), Reason: strings.Repeat("x", 500)})
		wantCode(t, err, codes.OK)
	})
}

func TestStoreErrorsAreSanitised(t *testing.T) {
	id := uuid.NewString()
	for _, method := range []string{"Insert", "Get", "List", "Transition"} {
		t.Run(method, func(t *testing.T) {
			s, fs := newServer(t)
			fs.failOn = method
			var err error
			switch method {
			case "Insert":
				_, err = s.ProcessPayment(alice(), payReq(id, 1, "USD", "k"))
			case "Get":
				_, err = s.GetPayment(alice(), &paymentsv1.GetPaymentRequest{Id: id})
			case "List":
				_, err = s.ListPayments(alice(), &paymentsv1.ListPaymentsRequest{})
			case "Transition":
				_, err = s.RefundPayment(admin(), &paymentsv1.RefundPaymentRequest{Id: id})
			}
			wantCode(t, err, codes.Internal)
			if m := status.Convert(err).Message(); m != "internal error" {
				t.Fatalf("leaked: %q", m)
			}
		})
	}
}
