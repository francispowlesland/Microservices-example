// Package service implements payments.v1.PaymentsService: request validation,
// authorisation and the payment state machine. Persistence sits behind Store.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
)

// Sentinel errors a Store returns. The service maps them to gRPC statuses.
var (
	// ErrNotFound means no payment row matches.
	ErrNotFound = errors.New("payment not found")
	// ErrStateConflict means the payment is not in the state the transition requires.
	ErrStateConflict = errors.New("payment not in required state")
)

// Payment is one ledger row. CreatedBy and RefundReason are stored but never
// returned over the wire (the proto has no field for them).
type Payment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	AmountCents    int64
	Currency       string
	Status         paymentsv1.PaymentStatus
	FailureReason  string
	RefundReason   string
	CreatedBy      uuid.UUID
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewPayment is a fully decided ledger row to insert.
type NewPayment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	AmountCents    int64
	Currency       string
	Status         paymentsv1.PaymentStatus
	FailureReason  string
	CreatedBy      uuid.UUID
	IdempotencyKey string
}

// ListFilter selects one page of payments, newest first.
type ListFilter struct {
	CreatedBy *uuid.UUID // nil = any owner (admin only)
	OrderID   *uuid.UUID // nil = any order
	// Cursor: when HasCursor, return rows strictly after (AfterCreatedAt, AfterID) in
	// (created_at DESC, id DESC) order.
	HasCursor      bool
	AfterCreatedAt time.Time
	AfterID        uuid.UUID
	Limit          int
}

// Store is the persistence contract. There is deliberately no update or delete
// of amount, currency or order: payments are a ledger.
type Store interface {
	// Insert stores n. If (CreatedBy, IdempotencyKey) already exists it inserts
	// nothing and returns the existing row with created=false. Concurrent calls
	// with the same key yield exactly one row.
	Insert(ctx context.Context, n NewPayment) (p Payment, created bool, err error)
	// Get returns ErrNotFound when the id is unknown.
	Get(ctx context.Context, id uuid.UUID) (Payment, error)
	List(ctx context.Context, f ListFilter) ([]Payment, error)
	// Transition atomically moves a payment from one status to another, locking
	// the row. It returns ErrNotFound for an unknown id and ErrStateConflict if the
	// current status is not from.
	Transition(ctx context.Context, id uuid.UUID, from, to paymentsv1.PaymentStatus, refundReason string) (Payment, error)
}
