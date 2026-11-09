package grpcx_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/grpcx"
)

type dummyUserService struct {
	userv1.UnimplementedUserServiceServer
	panicOnGetUser bool
}

func (s *dummyUserService) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	if s.panicOnGetUser {
		panic("database exploded")
	}
	claims, ok := authn.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}
	return &userv1.GetUserResponse{
		User: &userv1.User{
			Id: claims.Subject,
		},
	}, nil
}

func getFreeAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

func TestGrpcxRunAndHealthCheck(t *testing.T) {
	addr := getFreeAddr(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	verifier := authn.NewVerifier(pub, authn.Issuer, authn.Audience)
	signer := authn.NewSigner(priv, authn.Issuer, authn.Audience, 15*time.Minute)

	svc := &dummyUserService{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := grpcx.Config{
		Addr:         addr,
		AppEnv:       "development",
		AuthVerifier: verifier,
		PublicMethods: map[string]bool{
			"/grpc.health.v1.Health/Check": true,
			"/grpc.health.v1.Health/Watch": true,
		},
		ShutdownTimeout: 2 * time.Second,
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- grpcx.Run(ctx, cfg, func(server *grpc.Server) {
			userv1.RegisterUserServiceServer(server, svc)
		})
	}()

	// Wait for server to start and test HealthCheck
	var healthErr error
	for range 20 {
		time.Sleep(50 * time.Millisecond)
		if healthErr = grpcx.HealthCheck(addr, 1*time.Second); healthErr == nil {
			break
		}
	}
	if healthErr != nil {
		t.Fatalf("HealthCheck failed: %v", healthErr)
	}

	// Dial server
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	client := userv1.NewUserServiceClient(conn)

	// Unauthenticated request should fail with Unauthenticated
	_, err = client.GetUser(context.Background(), &userv1.GetUserRequest{Id: "123"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}

	// Authenticated request
	token, _, err := signer.Sign("user-abc", []string{"customer"})
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	authCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	resp, err := client.GetUser(authCtx, &userv1.GetUserRequest{Id: "user-abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetUser().GetId() != "user-abc" {
		t.Fatalf("expected user-abc, got %s", resp.GetUser().GetId())
	}

	// Test panic recovery
	svc.panicOnGetUser = true
	_, err = client.GetUser(authCtx, &userv1.GetUserRequest{Id: "user-abc"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal status code on panic, got %v", err)
	}
	if status.Convert(err).Message() != "internal error" {
		t.Fatalf("expected 'internal error' message, got %q", status.Convert(err).Message())
	}

	// Test graceful shutdown
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("grpcx.Run returned error on shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("grpcx.Run did not shut down within timeout")
	}
}

func TestHealthCheckNotServing(t *testing.T) {
	addr := getFreeAddr(t)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer lis.Close()

	s := grpc.NewServer()
	// Do not register health service or register it not serving
	go func() {
		_ = s.Serve(lis)
	}()
	defer s.Stop()

	time.Sleep(50 * time.Millisecond)
	err = grpcx.HealthCheck(addr, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when health service is not registered/serving")
	}
}
