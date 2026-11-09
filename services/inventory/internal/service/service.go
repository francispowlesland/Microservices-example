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
	ErrNotFound     = errors.New("inventory item not found")
	ErrDuplicateSKU = errors.New("duplicate sku")
)

// Item is the domain representation of a stock row.
type Item struct {
	ID        uuid.UUID
	SKU       string
	Quantity  int32
	CreatedAt time.Time
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
	Update(ctx context.Context, id uuid.UUID, sku string, quantity int32) (Item, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// List returns up to limit items ordered by (created_at desc, id desc),
	// strictly after cursor when it is non-nil.
	List(ctx context.Context, after *Cursor, limit int) ([]Item, error)
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
	sku, qty, err := validateItem(req.GetItem())
	if err != nil {
		return nil, err
	}
	it, err := s.store.Create(ctx, Item{ID: uuid.New(), SKU: sku, Quantity: qty})
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
	sku, qty, err := validateItem(req.GetItem())
	if err != nil {
		return nil, err
	}
	it, err := s.store.Update(ctx, id, sku, qty)
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

// validateItem returns the normalised SKU and the quantity.
func validateItem(it *inventoryv1.InventoryItem) (string, int32, error) {
	if it == nil {
		return "", 0, status.Error(codes.InvalidArgument, "item is required")
	}
	sku, ok := NormaliseSKU(it.GetSku())
	if !ok {
		return "", 0, status.Error(codes.InvalidArgument, "sku must match ^[A-Z0-9][A-Z0-9._-]{0,63}$ after trimming and upper-casing")
	}
	if it.GetQuantity() < 0 {
		return "", 0, status.Error(codes.InvalidArgument, "quantity must be >= 0")
	}
	return sku, it.GetQuantity(), nil
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
	default:
		s.logger.ErrorContext(ctx, "inventory store error", slog.String("op", op), slog.Any("error", err))
		return status.Error(codes.Internal, "internal error")
	}
}

func toProto(it Item) *inventoryv1.InventoryItem {
	return &inventoryv1.InventoryItem{Id: it.ID.String(), Sku: it.SKU, Quantity: it.Quantity}
}
