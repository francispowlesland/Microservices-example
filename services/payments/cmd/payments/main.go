// Command payments runs the payments.v1.PaymentsService gRPC server.
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

	paymentsv1 "github.com/francispowlesland/microservices-app-go/gen/go/payments/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
	"github.com/francispowlesland/microservices-app-go/internal/platform/grpcx"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	cfgpkg "github.com/francispowlesland/microservices-app-go/services/payments/internal/config"
	"github.com/francispowlesland/microservices-app-go/services/payments/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/payments/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/payments/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(logger); err != nil {
		logger.Error("payments exited", slog.Any("error", err))
		os.Exit(1)
	}
}

// healthcheck dials GRPC_ADDR on localhost and checks the standard health service.
func healthcheck() error {
	addr := config.String("GRPC_ADDR", ":50053")
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	return grpcx.HealthCheck(addr, 3*time.Second)
}

func run(logger *slog.Logger) error {
	cfg, err := cfgpkg.Load()
	if err != nil {
		return err
	}
	pub, err := authn.ParsePublicKeyPEM([]byte(cfg.JWTPublicKey))
	if err != nil {
		return fmt.Errorf("JWT_PUBLIC_KEY: %w", err)
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

	svc := service.New(store.New(pool), cfg.MaxCents, logger)
	logger.Info("payments starting", slog.String("addr", cfg.GRPCAddr), slog.String("env", cfg.AppEnv))
	return grpcx.Run(ctx, grpcx.Config{
		Addr:         cfg.GRPCAddr,
		AppEnv:       cfg.AppEnv,
		AuthVerifier: authn.NewVerifier(pub, authn.Issuer, authn.Audience),
		// grpcx's default-deny auth also blocks the unary health Check that the
		// Docker healthcheck uses, so exempt exactly that method.
		PublicMethods: map[string]bool{"/grpc.health.v1.Health/Check": true},
		Logger:        logger,
	}, func(s *grpc.Server) {
		paymentsv1.RegisterPaymentsServiceServer(s, svc)
	})
}
