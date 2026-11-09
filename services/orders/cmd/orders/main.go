// Command orders runs the orders.v1.OrdersService gRPC server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	ordersv1 "github.com/francispowlesland/microservices-app-go/gen/go/orders/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
	"github.com/francispowlesland/microservices-app-go/internal/platform/grpcx"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	ordersconfig "github.com/francispowlesland/microservices-app-go/services/orders/internal/config"
	inventoryclient "github.com/francispowlesland/microservices-app-go/services/orders/internal/inventory"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/orders/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/orders/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := healthAddr(config.String("GRPC_ADDR", ordersconfig.DefaultAddr))
		if err := grpcx.HealthCheck(addr, 3*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("orders exited", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// healthAddr turns a listen address such as ":50052" into a localhost dial address.
func healthAddr(listen string) string {
	if len(listen) > 0 && listen[0] == ':' {
		return "127.0.0.1" + listen
	}
	return listen
}

func run(logger *slog.Logger) error {
	cfg, err := ordersconfig.Load()
	if err != nil {
		return err
	}

	pub, err := authn.ParsePublicKeyPEM([]byte(cfg.JWTPublicKey))
	if err != nil {
		return fmt.Errorf("JWT_PUBLIC_KEY: %w", err)
	}
	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)

	svcKey, err := authn.ParsePrivateKeyPEM([]byte(cfg.ServicePrivateKey))
	if err != nil {
		return fmt.Errorf("SERVICE_PRIVATE_KEY: %w", err)
	}
	invConn, err := inventoryclient.Dial(cfg.InventoryAddr)
	if err != nil {
		return fmt.Errorf("INVENTORY_ADDR: %w", err)
	}
	defer invConn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := pgxutil.Migrate(ctx, cfg.DatabaseURL, migrations.FS, "."); err != nil {
		return err
	}
	pool, err := pgxutil.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := service.New(store.New(pool), inventoryclient.New(invConn, authn.NewServiceSigner(svcKey, time.Minute)), logger)
	logger.Info("orders listening", slog.String("addr", cfg.GRPCAddr), slog.String("env", cfg.AppEnv))
	return grpcx.Run(ctx, grpcx.Config{
		Addr:          cfg.GRPCAddr,
		AppEnv:        cfg.AppEnv,
		AuthVerifier:  verifier,
		PublicMethods: map[string]bool{"/grpc.health.v1.Health/Check": true},
		Logger:        logger,
	}, func(s *grpc.Server) {
		ordersv1.RegisterOrdersServiceServer(s, srv)
	})
}
