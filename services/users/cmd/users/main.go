// Command users runs the user.v1.UserService and user.v1.AuthService gRPC server.
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/config"
	"github.com/francispowlesland/microservices-app-go/internal/platform/grpcx"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	cfgpkg "github.com/francispowlesland/microservices-app-go/services/users/internal/config"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/password"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/store"
	"github.com/francispowlesland/microservices-app-go/services/users/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		addr := config.String("GRPC_ADDR", ":50051")
		if err := grpcx.HealthCheck("127.0.0.1"+portOf(addr), 3*time.Second); err != nil {
			logger.Error("healthcheck failed", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	if err := run(logger); err != nil {
		logger.Error("users exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := cfgpkg.Load()
	if err != nil {
		return err
	}
	priv, err := authn.ParsePrivateKeyPEM([]byte(cfg.JWTPrivateKey))
	if err != nil {
		return errors.New("JWT_PRIVATE_KEY is not a valid PKCS#8 Ed25519 PEM key")
	}
	pub := priv.Public().(ed25519.PublicKey)

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

	svc, err := service.New(service.Options{
		Store:      store.New(pool),
		Mailer:     service.LogMailer{Logger: logger},
		Signer:     authn.NewSigner(priv, authn.Issuer, authn.Audience, cfg.AccessTTL),
		Hasher:     password.NewHasher(password.Default),
		RefreshTTL: cfg.RefreshTTL,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	if cfg.BootstrapAdminEmail != "" {
		if err := svc.BootstrapAdmin(ctx, cfg.BootstrapAdminEmail, cfg.BootstrapAdminPassword); err != nil {
			return errors.New("bootstrap admin failed: " + err.Error())
		}
	}

	return grpcx.Run(ctx, grpcx.Config{
		Addr:          cfg.GRPCAddr,
		AppEnv:        cfg.AppEnv,
		AuthVerifier:  authn.NewVerifier(pub, authn.Issuer, authn.Audience),
		PublicMethods: service.PublicMethods(),
		Logger:        logger,
	}, func(s *grpc.Server) {
		userv1.RegisterUserServiceServer(s, svc)
		userv1.RegisterAuthServiceServer(s, svc)
	})
}

// portOf returns ":port" from an address such as ":50051" or "0.0.0.0:50051".
func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":50051"
}
