// Package inventory is the orders service's gRPC client for the inventory service.
package inventory

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
)

const (
	callTimeout = 5 * time.Second
	tokenTTL    = time.Minute
)

// Client implements service.Inventory. Every call carries a fresh service token.
type Client struct {
	rpc    inventoryv1.InventoryServiceClient
	signer *authn.Signer
}

var _ service.Inventory = (*Client)(nil)

// New wraps conn. signer must be an authn.NewServiceSigner.
func New(conn grpc.ClientConnInterface, signer *authn.Signer) *Client {
	return &Client{rpc: inventoryv1.NewInventoryServiceClient(conn), signer: signer}
}

// Dial opens a lazy plaintext connection, like every other hop in this system.
func Dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func (c *Client) call(ctx context.Context) (context.Context, context.CancelFunc, error) {
	tok, _, err := c.signer.Sign("orders", []string{"service"})
	if err != nil {
		return nil, nil, fmt.Errorf("sign service token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tok), cancel, nil
}

func (c *Client) Reserve(ctx context.Context, lines []service.Line) ([]service.Line, error) {
	ctx, cancel, err := c.call(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	resp, err := c.rpc.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{Lines: toProto(lines)})
	if err != nil {
		return nil, err
	}
	if len(resp.GetLines()) != len(lines) {
		return nil, fmt.Errorf("inventory returned %d lines for %d", len(resp.GetLines()), len(lines))
	}
	out := make([]service.Line, 0, len(lines))
	for _, l := range resp.GetLines() {
		out = append(out, service.Line{SKU: l.GetSku(), Quantity: l.GetQuantity(), UnitPriceCents: l.GetUnitPriceCents()})
	}
	return out, nil
}

func (c *Client) Release(ctx context.Context, lines []service.Line) error {
	ctx, cancel, err := c.call(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	_, err = c.rpc.ReleaseStock(ctx, &inventoryv1.ReleaseStockRequest{Lines: toProto(lines)})
	return err
}

func toProto(lines []service.Line) []*inventoryv1.StockLine {
	out := make([]*inventoryv1.StockLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, &inventoryv1.StockLine{Sku: l.SKU, Quantity: l.Quantity})
	}
	return out
}
