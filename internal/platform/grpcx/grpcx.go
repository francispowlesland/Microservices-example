package grpcx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

type Config struct {
	Addr            string
	AppEnv          string
	AuthVerifier    *authn.Verifier
	PublicMethods   map[string]bool
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

// Run listens on cfg.Addr, builds a gRPC server with recovery, logging, and authn interceptors,
// registers health and reflection (non-production), and performs graceful shutdown on context cancellation.
func Run(ctx context.Context, cfg Config, register func(*grpc.Server)) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("grpcx: listen on %s: %w", cfg.Addr, err)
	}
	defer lis.Close()

	interceptors := []grpc.UnaryServerInterceptor{
		recoveryInterceptor(logger),
		loggingInterceptor(logger),
	}

	if cfg.AuthVerifier != nil {
		interceptors = append(interceptors, authn.UnaryInterceptor(cfg.AuthVerifier, cfg.PublicMethods))
	}

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptors...))

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	if cfg.AppEnv != "production" {
		reflection.Register(server)
	}

	if register != nil {
		register(server)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownTimeout := cfg.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 5 * time.Second
		}

		stopped := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
		case <-time.After(shutdownTimeout):
			server.Stop()
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// HealthCheck dials addr and checks if the gRPC health service returns SERVING.
func HealthCheck(addr string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpcx: client for %s: %w", addr, err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpcx: health check failed: %w", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpcx: service status %s, expected SERVING", resp.Status)
	}
	return nil
}

func recoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logger.ErrorContext(ctx, "grpc panic recovered",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(stack)),
				)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func loggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		var peerAddr string
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			peerAddr = p.Addr.String()
		}

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		statusCode := status.Code(err)

		level := slog.LevelInfo
		if statusCode == codes.Internal || statusCode == codes.Unknown {
			level = slog.LevelError
		} else if statusCode != codes.OK {
			level = slog.LevelWarn
		}

		logger.Log(ctx, level, "grpc request",
			slog.String("method", info.FullMethod),
			slog.String("code", statusCode.String()),
			slog.Duration("duration", duration),
			slog.String("peer", peerAddr),
		)

		return resp, err
	}
}
