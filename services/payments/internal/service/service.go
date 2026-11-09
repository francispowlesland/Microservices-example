package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
)

const (
	maxIdempotencyKeyLen = 128
	maxRefundReasonLen   = 500
	// FailureReasonOverLimit is stored on payments the simulated processor declines.
	FailureReasonOverLimit = "amount exceeds limit"
)

var allowedCurrencies = map[string]bool{"USD": true, "EUR": true, "GBP": true}

// Server implements paymentsv1.PaymentsServiceServer.
type Server struct {
	paymentsv1.UnimplementedPaymentsServiceServer
	store    Store
	maxCents int64
	log      *slog.Logger
}

var _ paymentsv1.PaymentsServiceServer = (*Server)(nil)

// New returns a Server. maxCents is the largest amount the simulated processor accepts.
func New(store Store, maxCents int64, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{store: store, maxCents: maxCents, log: log}
}

// ProcessPayment records and settles a payment. Any authenticated user may call it.
func (s *Server) ProcessPayment(ctx context.Context, req *paymentsv1.ProcessPaymentRequest) (*paymentsv1.ProcessPaymentResponse, error) {
	caller, err := callerID(ctx)
	if err != nil {
		return nil, err
	}

	orderID, err := uuid.Parse(req.GetOrderId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "order_id must be a UUID")
	}
	if req.GetAmountCents() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_cents must be greater than zero")
	}
	if !allowedCurrencies[req.GetCurrency()] {
		return nil, status.Error(codes.InvalidArgument, "currency must be one of USD, EUR, GBP")
	}
	if !validIdempotencyKey(req.GetIdempotencyKey()) {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be 1 to 128 printable ASCII characters")
	}

	n := NewPayment{
		ID:             uuid.New(),
		OrderID:        orderID,
		AmountCents:    req.GetAmountCents(),
		Currency:       req.GetCurrency(),
		Status:         paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
		CreatedBy:      caller,
		IdempotencyKey: req.GetIdempotencyKey(),
	}
	// Simulated processor: deterministic, no external call.
	if n.AmountCents > s.maxCents {
		n.Status = paymentsv1.PaymentStatus_PAYMENT_STATUS_FAILED
		n.FailureReason = FailureReasonOverLimit
	}

	p, created, err := s.store.Insert(ctx, n)
	if err != nil {
		return nil, s.internal(ctx, "insert payment", err)
	}
	if !created && (p.OrderID != orderID || p.AmountCents != n.AmountCents || p.Currency != n.Currency) {
		return nil, status.Error(codes.InvalidArgument, "idempotency key reused with different parameters")
	}
	return &paymentsv1.ProcessPaymentResponse{Payment: toProto(p)}, nil
}

// GetPayment returns a payment to its owner or an admin. Other callers get NotFound.
func (s *Server) GetPayment(ctx context.Context, req *paymentsv1.GetPaymentRequest) (*paymentsv1.GetPaymentResponse, error) {
	claims, caller, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a UUID")
	}
	p, err := s.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, s.internal(ctx, "get payment", err)
	}
	if !claims.IsAdmin() && p.CreatedBy != caller {
		return nil, errNotFound
	}
	return &paymentsv1.GetPaymentResponse{Payment: toProto(p)}, nil
}

// ListPayments lists the caller's payments, or all payments for an admin.
// Filters come from the request and claims on every page, never from the token.
func (s *Server) ListPayments(ctx context.Context, req *paymentsv1.ListPaymentsRequest) (*paymentsv1.ListPaymentsResponse, error) {
	claims, caller, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}

	f := ListFilter{}
	if !claims.IsAdmin() {
		f.CreatedBy = &caller
	}
	if req.GetOrderId() != "" {
		oid, err := uuid.Parse(req.GetOrderId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "order_id must be a UUID")
		}
		f.OrderID = &oid
	}
	if req.GetPageToken() != "" {
		at, id, err := pagination.Decode(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		f.HasCursor, f.AfterCreatedAt, f.AfterID = true, at, id
	}
	size := pagination.PageSize(req.GetPageSize())
	f.Limit = size + 1

	rows, err := s.store.List(ctx, f)
	if err != nil {
		return nil, s.internal(ctx, "list payments", err)
	}
	resp := &paymentsv1.ListPaymentsResponse{}
	if len(rows) > size {
		rows = rows[:size]
		last := rows[size-1]
		resp.NextPageToken = pagination.Encode(last.CreatedAt, last.ID)
	}
	for _, p := range rows {
		resp.Payments = append(resp.Payments, toProto(p))
	}
	return resp, nil
}

// RefundPayment fully refunds a SUCCEEDED payment. Admin only.
func (s *Server) RefundPayment(ctx context.Context, req *paymentsv1.RefundPaymentRequest) (*paymentsv1.RefundPaymentResponse, error) {
	claims, _, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !claims.IsAdmin() {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "id must be a UUID")
	}
	reason := strings.TrimSpace(req.GetReason())
	if utf8.RuneCountInString(reason) > maxRefundReasonLen {
		return nil, status.Error(codes.InvalidArgument, "reason must be at most 500 characters")
	}

	p, err := s.store.Transition(ctx, id,
		paymentsv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
		paymentsv1.PaymentStatus_PAYMENT_STATUS_REFUNDED, reason)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, errNotFound
	case errors.Is(err, ErrStateConflict):
		return nil, status.Error(codes.FailedPrecondition, "only SUCCEEDED payments can be refunded")
	case err != nil:
		return nil, s.internal(ctx, "refund payment", err)
	}
	return &paymentsv1.RefundPaymentResponse{Payment: toProto(p)}, nil
}

var errNotFound = status.Error(codes.NotFound, "payment not found")

func (s *Server) internal(ctx context.Context, op string, err error) error {
	s.log.ErrorContext(ctx, "payments store error", slog.String("op", op), slog.Any("error", err))
	return status.Error(codes.Internal, "internal error")
}

// callerID returns the caller's JWT subject as a UUID.
func callerID(ctx context.Context) (uuid.UUID, error) {
	_, id, err := callerClaims(ctx)
	return id, err
}

func callerClaims(ctx context.Context) (authn.Claims, uuid.UUID, error) {
	claims := authn.MustFromContext(ctx)
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return authn.Claims{}, uuid.Nil, status.Error(codes.Unauthenticated, "invalid token subject")
	}
	return claims, id, nil
}

func validIdempotencyKey(k string) bool {
	if len(k) < 1 || len(k) > maxIdempotencyKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func toProto(p Payment) *paymentsv1.Payment {
	return &paymentsv1.Payment{
		Id:            p.ID.String(),
		OrderId:       p.OrderID.String(),
		AmountCents:   p.AmountCents,
		Currency:      p.Currency,
		Status:        p.Status,
		FailureReason: p.FailureReason,
	}
}
