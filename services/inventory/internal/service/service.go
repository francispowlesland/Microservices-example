// Package service implements inventory.v1.InventoryService business rules.
package service

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
)

// Errors a Store must return so the service can map them to gRPC codes.
var (
	ErrNotFound          = errors.New("inventory item not found")
	ErrDuplicateSKU      = errors.New("duplicate sku")
	ErrUnknownSKU        = errors.New("unknown sku")
	ErrInsufficientStock = errors.New("insufficient stock")
)

const (
	// MaxPriceCents bounds a unit price so quantity*price cannot overflow int64 in any caller.
	MaxPriceCents int64 = 1_000_000_000
	// MaxStockLines bounds one Reserve/Release call.
	MaxStockLines = 100
)

// Item is the domain representation of a stock row.
type Item struct {
	ID         uuid.UUID
	SKU        string
	Quantity   int32
	PriceCents int64
	CreatedAt  time.Time
}

// StockLine is one SKU and quantity in a Reserve or Release call. Reserve fills UnitPriceCents.
type StockLine struct {
	SKU            string
	Quantity       int32
	UnitPriceCents int64
}

// Cursor is the keyset position after which a List page starts.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// Store is the persistence contract. Implementations return ErrNotFound and
// ErrDuplicateSKU where relevant; any other error is treated as internal.
type Store interface {
	Create(ctx context.Context, it Item) (Item, error)
	Get(ctx context.Context, id uuid.UUID) (Item, error)
	Update(ctx context.Context, id uuid.UUID, sku string, quantity int32, priceCents int64) (Item, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// List returns up to limit items ordered by (created_at desc, id desc),
	// strictly after cursor when it is non-nil.
	List(ctx context.Context, after *Cursor, limit int) ([]Item, error)
	// Reserve takes stock for every line in one transaction or for none. It returns the lines
	// in input order with UnitPriceCents set, or an error wrapping ErrUnknownSKU or ErrInsufficientStock.
	Reserve(ctx context.Context, lines []StockLine) ([]StockLine, error)
	// Release puts stock back for every line in one transaction. SKUs that no longer exist are skipped.
	Release(ctx context.Context, lines []StockLine) error
}

var skuPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,63}$`)

// Service implements inventoryv1.InventoryServiceServer.
type Service struct {
	inventoryv1.UnimplementedInventoryServiceServer
	store  Store
	logger *slog.Logger
}

func New(store Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, logger: logger}
}

func (s *Service) CreateInventoryItem(ctx context.Context, req *inventoryv1.CreateInventoryItemRequest) (*inventoryv1.CreateInventoryItemResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	sku, qty, price, err := validateItem(req.GetItem())
	if err != nil {
		return nil, err
	}
	it, err := s.store.Create(ctx, Item{ID: uuid.New(), SKU: sku, Quantity: qty, PriceCents: price})
	if err != nil {
		return nil, s.mapErr(ctx, "create", err)
	}
	return &inventoryv1.CreateInventoryItemResponse{Item: toProto(it)}, nil
}

func (s *Service) GetInventoryItem(ctx context.Context, req *inventoryv1.GetInventoryItemRequest) (*inventoryv1.GetInventoryItemResponse, error) {
	authn.MustFromContext(ctx)
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	it, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, s.mapErr(ctx, "get", err)
	}
	return &inventoryv1.GetInventoryItemResponse{Item: toProto(it)}, nil
}

func (s *Service) UpdateInventoryItem(ctx context.Context, req *inventoryv1.UpdateInventoryItemRequest) (*inventoryv1.UpdateInventoryItemResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := parseID(req.GetItem().GetId())
	if err != nil {
		return nil, err
	}
	sku, qty, price, err := validateItem(req.GetItem())
	if err != nil {
		return nil, err
	}
	it, err := s.store.Update(ctx, id, sku, qty, price)
	if err != nil {
		return nil, s.mapErr(ctx, "update", err)
	}
	return &inventoryv1.UpdateInventoryItemResponse{Item: toProto(it)}, nil
}

func (s *Service) DeleteInventoryItem(ctx context.Context, req *inventoryv1.DeleteInventoryItemRequest) (*inventoryv1.DeleteInventoryItemResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return nil, s.mapErr(ctx, "delete", err)
	}
	return &inventoryv1.DeleteInventoryItemResponse{}, nil
}

func (s *Service) ListInventoryItems(ctx context.Context, req *inventoryv1.ListInventoryItemsRequest) (*inventoryv1.ListInventoryItemsResponse, error) {
	authn.MustFromContext(ctx)
	var after *Cursor
	if req.GetPageToken() != "" {
		ts, id, err := pagination.Decode(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		after = &Cursor{CreatedAt: ts, ID: id}
	}
	size := pagination.PageSize(req.GetPageSize())
	items, err := s.store.List(ctx, after, size+1)
	if err != nil {
		return nil, s.mapErr(ctx, "list", err)
	}
	resp := &inventoryv1.ListInventoryItemsResponse{}
	if len(items) > size {
		items = items[:size]
		last := items[size-1]
		resp.NextPageToken = pagination.Encode(last.CreatedAt, last.ID)
	}
	resp.Items = make([]*inventoryv1.InventoryItem, 0, len(items))
	for _, it := range items {
		resp.Items = append(resp.Items, toProto(it))
	}
	return resp, nil
}

// ReserveStock takes stock for every line or none. Service identity only.
func (s *Service) ReserveStock(ctx context.Context, req *inventoryv1.ReserveStockRequest) (*inventoryv1.ReserveStockResponse, error) {
	if err := requireService(ctx); err != nil {
		return nil, err
	}
	lines, err := validateLines(req.GetLines())
	if err != nil {
		return nil, err
	}
	got, err := s.store.Reserve(ctx, lines)
	if err != nil {
		return nil, s.mapErr(ctx, "reserve", err)
	}
	return &inventoryv1.ReserveStockResponse{Lines: linesToProto(got)}, nil
}

// ReleaseStock puts stock back for every line. Service identity only.
func (s *Service) ReleaseStock(ctx context.Context, req *inventoryv1.ReleaseStockRequest) (*inventoryv1.ReleaseStockResponse, error) {
	if err := requireService(ctx); err != nil {
		return nil, err
	}
	lines, err := validateLines(req.GetLines())
	if err != nil {
		return nil, err
	}
	if err := s.store.Release(ctx, lines); err != nil {
		return nil, s.mapErr(ctx, "release", err)
	}
	return &inventoryv1.ReleaseStockResponse{}, nil
}

func requireService(ctx context.Context) error {
	if !authn.MustFromContext(ctx).Service {
		return status.Error(codes.PermissionDenied, "service identity required")
	}
	return nil
}

func validateLines(in []*inventoryv1.StockLine) ([]StockLine, error) {
	if len(in) == 0 || len(in) > MaxStockLines {
		return nil, status.Errorf(codes.InvalidArgument, "lines must have 1 to %d entries", MaxStockLines)
	}
	seen := make(map[string]bool, len(in))
	out := make([]StockLine, 0, len(in))
	for _, l := range in {
		sku, ok := NormaliseSKU(l.GetSku())
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "line sku is invalid")
		}
		if l.GetQuantity() < 1 {
			return nil, status.Errorf(codes.InvalidArgument, "%s: quantity must be >= 1", sku)
		}
		if seen[sku] {
			return nil, status.Errorf(codes.InvalidArgument, "%s: duplicate sku in lines", sku)
		}
		seen[sku] = true
		out = append(out, StockLine{SKU: sku, Quantity: l.GetQuantity()})
	}
	return out, nil
}

func linesToProto(lines []StockLine) []*inventoryv1.StockLine {
	out := make([]*inventoryv1.StockLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, &inventoryv1.StockLine{Sku: l.SKU, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents})
	}
	return out
}

func requireAdmin(ctx context.Context) error {
	if !authn.MustFromContext(ctx).IsAdmin() {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	return nil
}

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	return id, nil
}

// validateItem returns the normalised SKU, the quantity and the unit price.
func validateItem(it *inventoryv1.InventoryItem) (string, int32, int64, error) {
	if it == nil {
		return "", 0, 0, status.Error(codes.InvalidArgument, "item is required")
	}
	sku, ok := NormaliseSKU(it.GetSku())
	if !ok {
		return "", 0, 0, status.Error(codes.InvalidArgument, "sku must match ^[A-Z0-9][A-Z0-9._-]{0,63}$ after trimming and upper-casing")
	}
	if it.GetQuantity() < 0 {
		return "", 0, 0, status.Error(codes.InvalidArgument, "quantity must be >= 0")
	}
	if p := it.GetPriceCents(); p < 0 || p > MaxPriceCents {
		return "", 0, 0, status.Errorf(codes.InvalidArgument, "price_cents must be 0..%d", MaxPriceCents)
	}
	return sku, it.GetQuantity(), it.GetPriceCents(), nil
}

// NormaliseSKU trims whitespace and upper-cases ASCII letters only (so
// non-ASCII letters such as dotless i cannot fold into valid characters),
// then reports whether the result is a valid SKU.
func NormaliseSKU(raw string) (string, bool) {
	b := []byte(strings.TrimSpace(raw))
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	sku := string(b)
	return sku, skuPattern.MatchString(sku)
}

func (s *Service) mapErr(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "inventory item not found")
	case errors.Is(err, ErrDuplicateSKU):
		return status.Error(codes.AlreadyExists, "sku already exists")
	case errors.Is(err, ErrUnknownSKU):
		return status.Error(codes.NotFound, err.Error()) // store wraps with "<SKU>: unknown sku"
	case errors.Is(err, ErrInsufficientStock):
		return status.Error(codes.FailedPrecondition, err.Error()) // "<SKU>: insufficient stock"
	default:
		s.logger.ErrorContext(ctx, "inventory store error", slog.String("op", op), slog.Any("error", err))
		return status.Error(codes.Internal, "internal error")
	}
}

func toProto(it Item) *inventoryv1.InventoryItem {
	return &inventoryv1.InventoryItem{Id: it.ID.String(), Sku: it.SKU, Quantity: it.Quantity, PriceCents: it.PriceCents}
}
