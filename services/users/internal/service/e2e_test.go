package service

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

// TestEndToEndOverBufconn drives the real handlers through the real authn
// interceptor and gRPC transport: signup, verify, login, authenticated read,
// refresh, logout.
func TestEndToEndOverBufconn(t *testing.T) {
	h := newHarness(t)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnaryInterceptor(authn.UnaryInterceptor(h.verify, PublicMethods())))
	userv1.RegisterUserServiceServer(srv, h.svc)
	userv1.RegisterAuthServiceServer(srv, h.svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	users, auth := userv1.NewUserServiceClient(conn), userv1.NewAuthServiceClient(conn)
	ctx := context.Background()

	// Protected methods are default-deny without a token.
	_, err = users.ListUsers(ctx, &userv1.ListUsersRequest{})
	wantCode(t, err, codes.Unauthenticated)

	// Sign up (public).
	created, err := users.CreateUser(ctx, &userv1.CreateUserRequest{Email: "e2e@example.com", Username: "e2e", Name: "E2E", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	id := created.User.Id

	// Login before verification is refused.
	_, err = auth.Login(ctx, &userv1.LoginRequest{Identifier: "e2e", Password: testPassword})
	wantCode(t, err, codes.FailedPrecondition)

	// Verify with the mailed token (the LogMailer substitute).
	h.svc.flush()
	vt, ok := h.mailer.last(KindVerify, "e2e@example.com")
	if !ok {
		t.Fatal("no verification mail")
	}
	if _, err = auth.VerifyEmail(ctx, &userv1.VerifyEmailRequest{VerificationToken: vt}); err != nil {
		t.Fatal(err)
	}

	// Login.
	login, err := auth.Login(ctx, &userv1.LoginRequest{Identifier: "e2e@example.com", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	authed := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+login.Tokens.AccessToken)

	// GetUser with the access token; someone else's id is NotFound.
	got, err := users.GetUser(authed, &userv1.GetUserRequest{Id: id})
	if err != nil || got.User.Username != "e2e" {
		t.Fatalf("GetUser: %v %v", got, err)
	}
	_, err = users.GetUser(authed, &userv1.GetUserRequest{Id: "00000000-0000-0000-0000-000000000001"})
	wantCode(t, err, codes.NotFound)
	_, err = users.ListUsers(authed, &userv1.ListUsersRequest{})
	wantCode(t, err, codes.PermissionDenied)

	// A garbage bearer token is rejected.
	bad := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer nope")
	_, err = users.GetUser(bad, &userv1.GetUserRequest{Id: id})
	wantCode(t, err, codes.Unauthenticated)

	// Refresh rotates; the old token is dead.
	ref, err := auth.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: login.Tokens.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}

	// Logout (public) revokes the family; the rotated token no longer works.
	if _, err = auth.Logout(ctx, &userv1.LogoutRequest{RefreshToken: ref.Tokens.RefreshToken}); err != nil {
		t.Fatal(err)
	}
	_, err = auth.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: ref.Tokens.RefreshToken})
	wantCode(t, err, codes.Unauthenticated)

	// ChangePassword needs a token.
	_, err = auth.ChangePassword(ctx, &userv1.ChangePasswordRequest{UserId: id})
	wantCode(t, err, codes.Unauthenticated)
}
