// Package service implements orders.v1.OrdersService: validation, authorisation
// and business rules. Persistence is behind the Store interface.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ordersv1 "github.com/francispowlesland/microservices-app-go/gen/go/orders/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
)

const (
	// MaxTotalCents is the largest accepted order total (10^12 minor units).
	MaxTotalCents int64 = 1_000_000_000_000
	// MaxLines is the most lines one order may have.
	MaxLines = 100
)

// ErrNotFound is returned by a Store when no row matches.
var ErrNotFound = errors.New("order not found")

// Line is one SKU on an order. UnitPriceCents is the price when the order was placed.
type Line struct {
	SKU            string
	Quantity       int32
	UnitPriceCents int64
}

// Order is the domain model. CreatedAt is populated by the store and used only
// for keyset ordering; it is not exposed on the wire (the contract has no timestamps).
type Order struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TotalCents int64
	Lines      []Line
	CreatedAt  time.Time
}

// ListParams selects one page. Limit is the number of rows to fetch (callers pass size+1).
type ListParams struct {
	UserID *uuid.UUID // nil means all users (admin only)

	HasCursor      bool
	AfterCreatedAt time.Time
	AfterID        uuid.UUID
	Limit          int
}

// Store is the persistence port. Implementations return ErrNotFound for missing rows.
type Store interface {
	Create(ctx context.Context, o Order) (Order, error)
	Get(ctx context.Context, id uuid.UUID) (Order, error)
	// Update reassigns the order to o.UserID. Total and lines are never changed.
	Update(ctx context.Context, o Order) (Order, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, p ListParams) ([]Order, error)
}

// Inventory is the stock port. Implementations return gRPC status errors: InvalidArgument,
// NotFound (unknown SKU) and FailedPrecondition (insufficient stock) are shown to the caller;
// anything else is treated as an inventory outage.
type Inventory interface {
	// Reserve takes stock for every line or none and returns the lines with unit prices and
	// normalised SKUs, in input order.
	Reserve(ctx context.Context, lines []Line) ([]Line, error)
	Release(ctx context.Context, lines []Line) error
}

// Server implements ordersv1.OrdersServiceServer.
type Server struct {
	ordersv1.UnimplementedOrdersServiceServer
	store     Store
	inventory Inventory
	logger    *slog.Logger
}

var _ ordersv1.OrdersServiceServer = (*Server)(nil)

// New returns a Server. A nil logger uses slog.Default.
func New(store Store, inventory Inventory, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: store, inventory: inventory, logger: logger}
}

func (s *Server) internal(ctx context.Context, op string, err error) error {
	s.logger.ErrorContext(ctx, "orders store error", slog.String("op", op), slog.String("error", err.Error()))
	return status.Error(codes.Internal, "internal error")
}

// caller returns the authenticated caller's claims and parsed subject.
func caller(ctx context.Context) (authn.Claims, uuid.UUID, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok {
		return authn.Claims{}, uuid.Nil, status.Error(codes.Unauthenticated, "unauthenticated")
	}
	sub, err := uuid.Parse(claims.Subject)
	if err != nil {
		return authn.Claims{}, uuid.Nil, status.Error(codes.Unauthenticated, "invalid token subject")
	}
	return claims, sub, nil
}

func parseID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(v))
	if err != nil {
		return uuid.Nil, status.Errorf(codes.InvalidArgument, "%s must be a valid UUID", field)
	}
	return id, nil
}

func validateLines(in []*ordersv1.OrderLine) ([]Line, error) {
	if len(in) == 0 || len(in) > MaxLines {
		return nil, status.Errorf(codes.InvalidArgument, "an order needs 1 to %d lines", MaxLines)
	}
	out := make([]Line, 0, len(in))
	for _, l := range in {
		sku := strings.TrimSpace(l.GetSku())
		if sku == "" {
			return nil, status.Error(codes.InvalidArgument, "line sku is required")
		}
		if l.GetQuantity() < 1 {
			return nil, status.Errorf(codes.InvalidArgument, "%s: quantity must be >= 1", sku)
		}
		out = append(out, Line{SKU: sku, Quantity: l.GetQuantity()})
	}
	return out, nil
}

// total sums quantity * unit price, stopping as soon as it passes MaxTotalCents so it cannot overflow.
func total(lines []Line) (int64, error) {
	var sum int64
	for _, l := range lines {
		sum += int64(l.Quantity) * l.UnitPriceCents
		if sum > MaxTotalCents {
			return 0, status.Errorf(codes.InvalidArgument, "order total must be <= %d cents", MaxTotalCents)
		}
	}
	return sum, nil
}

// inventoryErr maps a failure from the inventory service to what the caller may see.
func (s *Server) inventoryErr(ctx context.Context, op string, err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition:
		return status.Error(status.Code(err), status.Convert(err).Message())
	case codes.NotFound:
		return status.Error(codes.InvalidArgument, status.Convert(err).Message())
	case codes.Unavailable, codes.DeadlineExceeded:
		s.logger.ErrorContext(ctx, "inventory unavailable", slog.String("op", op), slog.String("error", err.Error()))
		return status.Error(codes.Unavailable, "stock service is not responding")
	}
	return s.internal(ctx, "inventory "+op, err)
}

// release puts stock back after the order it was reserved for did not happen.
// shortcut: a failed release is only logged (with the lines), so stock stays missing until an admin
// corrects it by hand; upgrade to a retry queue or outbox. A Reserve that timed out may also have
// committed unseen; fixing that needs an idempotency key on ReserveStock.
func (s *Server) release(ctx context.Context, lines []Line) {
	if len(lines) == 0 {
		return
	}
	if err := s.inventory.Release(context.WithoutCancel(ctx), lines); err != nil {
		s.logger.ErrorContext(ctx, "stock release failed, stock needs manual correction",
			slog.Any("lines", lines), slog.String("error", err.Error()))
	}
}

func toProto(o Order) *ordersv1.Order {
	lines := make([]*ordersv1.OrderLine, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, &ordersv1.OrderLine{Sku: l.SKU, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents})
	}
	return &ordersv1.Order{Id: o.ID.String(), UserId: o.UserID.String(), TotalCents: o.TotalCents, Lines: lines}
}

// CreateOrder reserves stock for the lines, then creates the order. The id is always generated
// server-side and the total is always computed from inventory prices; client values are ignored.
func (s *Server) CreateOrder(ctx context.Context, req *ordersv1.CreateOrderRequest) (*ordersv1.CreateOrderResponse, error) {
	claims, sub, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetOrder()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "order is required")
	}
	lines, err := validateLines(in.GetLines())
	if err != nil {
		return nil, err
	}
	userID := sub
	if raw := strings.TrimSpace(in.GetUserId()); raw != "" {
		userID, err = parseID("user_id", raw)
		if err != nil {
			return nil, err
		}
		if userID != sub && !claims.IsAdmin() {
			return nil, status.Error(codes.PermissionDenied, "cannot create orders for another user")
		}
	}
	reserved, err := s.inventory.Reserve(ctx, lines)
	if err != nil {
		return nil, s.inventoryErr(ctx, "reserve", err)
	}
	sum, err := total(reserved)
	if err != nil {
		s.release(ctx, reserved)
		return nil, err
	}
	got, err := s.store.Create(ctx, Order{ID: uuid.New(), UserID: userID, TotalCents: sum, Lines: reserved})
	if err != nil {
		s.release(ctx, reserved)
		return nil, s.internal(ctx, "create", err)
	}
	return &ordersv1.CreateOrderResponse{Order: toProto(got)}, nil
}

// GetOrder returns an order to its owner or an admin; others get NotFound.
func (s *Server) GetOrder(ctx context.Context, req *ordersv1.GetOrderRequest) (*ordersv1.GetOrderResponse, error) {
	claims, sub, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	got, err := s.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, s.internal(ctx, "get", err)
	}
	if got.UserID != sub && !claims.IsAdmin() {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &ordersv1.GetOrderResponse{Order: toProto(got)}, nil
}

// UpdateOrder reassigns the order to another user. Admin only. Lines and total are fixed once placed.
func (s *Server) UpdateOrder(ctx context.Context, req *ordersv1.UpdateOrderRequest) (*ordersv1.UpdateOrderResponse, error) {
	claims, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if !claims.IsAdmin() {
		return nil, status.Error(codes.PermissionDenied, "admin only")
	}
	in := req.GetOrder()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "order is required")
	}
	id, err := parseID("id", in.GetId())
	if err != nil {
		return nil, err
	}
	userID, err := parseID("user_id", in.GetUserId())
	if err != nil {
		return nil, err
	}
	got, err := s.store.Update(ctx, Order{ID: id, UserID: userID})
	if errors.Is(err, ErrNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, s.internal(ctx, "update", err)
	}
	return &ordersv1.UpdateOrderResponse{Order: toProto(got)}, nil
}

// DeleteOrder removes an order and puts its stock back. Admin only.
func (s *Server) DeleteOrder(ctx context.Context, req *ordersv1.DeleteOrderRequest) (*ordersv1.DeleteOrderResponse, error) {
	claims, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if !claims.IsAdmin() {
		return nil, status.Error(codes.PermissionDenied, "admin only")
	}
	id, err := parseID("id", req.GetId())
	if err != nil {
		return nil, err
	}
	existing, err := s.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, s.internal(ctx, "delete", err)
	}
	// Release only after our own delete succeeded, so two racing deletes cannot both return stock.
	err = s.store.Delete(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, s.internal(ctx, "delete", err)
	}
	s.release(ctx, existing.Lines)
	return &ordersv1.DeleteOrderResponse{}, nil
}

// ListOrders lists orders newest first. Non-admins are always restricted to themselves.
// The filter is taken from the request and claims on every page, never from the page token.
func (s *Server) ListOrders(ctx context.Context, req *ordersv1.ListOrdersRequest) (*ordersv1.ListOrdersResponse, error) {
	claims, sub, err := caller(ctx)
	if err != nil {
		return nil, err
	}

	var filter *uuid.UUID
	if raw := strings.TrimSpace(req.GetUserId()); raw != "" {
		uid, err := parseID("user_id", raw)
		if err != nil {
			return nil, err
		}
		if uid != sub && !claims.IsAdmin() {
			return nil, status.Error(codes.PermissionDenied, "cannot list another user's orders")
		}
		filter = &uid
	}
	if !claims.IsAdmin() {
		filter = &sub
	}

	params := ListParams{UserID: filter}
	if req.GetPageToken() != "" {
		at, afterID, err := pagination.Decode(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		params.HasCursor, params.AfterCreatedAt, params.AfterID = true, at, afterID
	}
	size := pagination.PageSize(req.GetPageSize())
	params.Limit = size + 1

	rows, err := s.store.List(ctx, params)
	if err != nil {
		return nil, s.internal(ctx, "list", err)
	}

	resp := &ordersv1.ListOrdersResponse{}
	if len(rows) > size {
		rows = rows[:size]
		last := rows[size-1]
		resp.NextPageToken = pagination.Encode(last.CreatedAt, last.ID)
	}
	resp.Orders = make([]*ordersv1.Order, 0, len(rows))
	for _, o := range rows {
		resp.Orders = append(resp.Orders, toProto(o))
	}
	return resp, nil
}
