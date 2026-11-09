// Command inventory runs the inventory.v1.InventoryService gRPC server.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	inventoryv1 "github.com/francispowlesland/microservices-app-go/gen/go/inventory/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
	"github.com/francispowlesland/microservices-app-go/internal/platform/grpcx"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	cfgpkg "github.com/francispowlesland/microservices-app-go/services/inventory/internal/config"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/inventory/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/inventory/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := grpcx.HealthCheck("127.0.0.1"+portOf(config.String("GRPC_ADDR", ":50054")), 3*time.Second); err != nil {
			logger.Error("healthcheck failed", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	if err := run(logger); err != nil {
		logger.Error("inventory exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := cfgpkg.Load()
	if err != nil {
		return err
	}
	pub, err := authn.ParsePublicKeyPEM([]byte(cfg.JWTPublic))
	if err != nil {
		return err
	}

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

	svc := service.New(store.New(pool), logger)
	return grpcx.Run(ctx, grpcx.Config{
		Addr:         cfg.GRPCAddr,
		AppEnv:       cfg.AppEnv,
		AuthVerifier: authn.NewVerifier(pub, authn.Issuer, authn.Audience),
		Logger:       logger,
	}, func(s *grpc.Server) {
		inventoryv1.RegisterInventoryServiceServer(s, svc)
	})
}

// portOf returns ":port" from an address such as ":50054" or "0.0.0.0:50054".
func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return ":50054"
}
