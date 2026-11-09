package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
)

type fakeRegistrar struct{ descs []*grpc.ServiceDesc }

func (r *fakeRegistrar) RegisterService(d *grpc.ServiceDesc, _ any) { r.descs = append(r.descs, d) }

func TestLoginSuccess(t *testing.T) {
	h := newHarness(t)
	u := h.active("alice")
	resp, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "  Alice@Example.com ", Password: testPassword})
	wantCode(t, err, codes.OK)
	if resp.User.Id != u.ID.String() || resp.User.LastLoginAt == nil {
		t.Fatalf("user = %+v", resp.User)
	}
	claims, err := h.verify.Verify(resp.Tokens.AccessToken)
	if err != nil || claims.Subject != u.ID.String() || !claims.HasRole(RoleCustomer) {
		t.Fatalf("claims = %+v, err %v", claims, err)
	}
	if resp.Tokens.RefreshToken == "" || resp.Tokens.RefreshTokenExpiresAt == nil || resp.Tokens.AccessTokenExpiresAt == nil {
		t.Fatalf("tokens incomplete: %v", resp.Tokens)
	}
	// By username too.
	h.login("alice")
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	h := newHarness(t)
	h.active("alice")
	bad := func(ident, pw string) error {
		_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: ident, Password: pw})
		return err
	}
	unknown := bad("ghost@example.com", testPassword)
	unknownName := bad("ghost", testPassword)
	wrong := bad("alice", "wrong-password-123")
	empty := bad("", "")
	for name, err := range map[string]error{"unknown email": unknown, "unknown username": unknownName, "wrong password": wrong, "empty": empty} {
		wantCode(t, err, codes.Unauthenticated)
		if status.Convert(err).Message() != "invalid credentials" {
			t.Errorf("%s: message %q", name, status.Convert(err).Message())
		}
	}
}

func TestLoginLockout(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	for i := 0; i < 5; i++ {
		_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: "wrong-password-123"})
		wantCode(t, err, codes.Unauthenticated)
	}
	if u := h.store.user(alice.ID); u.LockedUntil == nil || u.FailedLoginCount != 5 {
		t.Fatalf("expected lock after 5 failures: %+v", u)
	}
	// Locked: even the correct password gets the same generic error.
	_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: testPassword})
	wantCode(t, err, codes.Unauthenticated)
	if status.Convert(err).Message() != "invalid credentials" {
		t.Fatalf("locked message = %q", status.Convert(err).Message())
	}
	// Still locked just before expiry, open just after.
	h.clock.Advance(15*time.Minute - time.Second)
	_, err = h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: testPassword})
	wantCode(t, err, codes.Unauthenticated)
	h.clock.Advance(2 * time.Second)
	h.login("alice")
	if u := h.store.user(alice.ID); u.FailedLoginCount != 0 || u.LockedUntil != nil {
		t.Fatalf("counter not reset: %+v", u)
	}
}

func TestLoginFourFailuresThenSuccessResets(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	for i := 0; i < 4; i++ {
		_, _ = h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: "wrong-password-123"})
	}
	h.login("alice")
	if h.store.user(alice.ID).FailedLoginCount != 0 {
		t.Fatal("counter not reset on success")
	}
}

func TestLoginStatusGates(t *testing.T) {
	h := newHarness(t)
	h.seed("pending", userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION)
	h.seed("banned", userv1.UserStatus_USER_STATUS_SUSPENDED)
	tests := []struct {
		ident, pw string
		want      codes.Code
	}{
		{"pending", testPassword, codes.FailedPrecondition},
		{"banned", testPassword, codes.PermissionDenied},
		// Status is only revealed after the password is proven.
		{"pending", "wrong-password-123", codes.Unauthenticated},
		{"banned", "wrong-password-123", codes.Unauthenticated},
	}
	for _, tt := range tests {
		_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: tt.ident, Password: tt.pw})
		wantCode(t, err, tt.want)
	}
}

func TestLoginStoreFailureIsInternal(t *testing.T) {
	h := newHarness(t)
	h.store.failNext = errors.New("connection refused to 10.0.0.5")
	_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: testPassword})
	wantCode(t, err, codes.Internal)
	if status.Convert(err).Message() != "internal error" {
		t.Fatalf("leaked: %q", status.Convert(err).Message())
	}
}

func TestRefreshRotation(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	t1 := h.login("alice")

	r1, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: t1.RefreshToken})
	wantCode(t, err, codes.OK)
	if r1.Tokens.RefreshToken == t1.RefreshToken || r1.Tokens.AccessToken == "" {
		t.Fatal("refresh token not rotated")
	}
	if n := h.store.activeRefreshCount(alice.ID); n != 1 {
		t.Fatalf("active refresh tokens = %d, want 1", n)
	}
	r2, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: r1.Tokens.RefreshToken})
	wantCode(t, err, codes.OK)
	if r2.Tokens.RefreshToken == r1.Tokens.RefreshToken {
		t.Fatal("not rotated twice")
	}
}

func TestRefreshReuseRevokesFamily(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	t1 := h.login("alice")
	other := h.login("alice") // second, independent family
	r1, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: t1.RefreshToken})
	wantCode(t, err, codes.OK)

	// Replaying the old token is theft: it must fail and kill the family...
	_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: t1.RefreshToken})
	wantCode(t, err, codes.Unauthenticated)
	// ...including the newest token the legitimate holder has.
	_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: r1.Tokens.RefreshToken})
	wantCode(t, err, codes.Unauthenticated)
	// The other family is untouched.
	if n := h.store.activeRefreshCount(alice.ID); n != 1 {
		t.Fatalf("active tokens = %d, want only the other family's 1", n)
	}
	_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: other.RefreshToken})
	wantCode(t, err, codes.OK)
}

func TestRefreshRejections(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	tok := h.login("alice")

	_, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{})
	wantCode(t, err, codes.Unauthenticated)
	_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: "unknown"})
	wantCode(t, err, codes.Unauthenticated)

	t.Run("suspended since login", func(t *testing.T) {
		h.store.setUser(alice.ID, func(u *User) { u.Status = userv1.UserStatus_USER_STATUS_SUSPENDED })
		_, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
		wantCode(t, err, codes.Unauthenticated)
		h.store.setUser(alice.ID, func(u *User) { u.Status = userv1.UserStatus_USER_STATUS_ACTIVE })
	})
	t.Run("expired", func(t *testing.T) {
		h.clock.Advance(time.Hour + time.Second)
		_, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
		wantCode(t, err, codes.Unauthenticated)
	})
}

func TestRefreshRereadsRoles(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	tok := h.login("alice")
	h.store.setUser(alice.ID, func(u *User) { u.Roles = []string{RoleAdmin, RoleCustomer} })
	r, err := h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
	wantCode(t, err, codes.OK)
	claims, err := h.verify.Verify(r.Tokens.AccessToken)
	if err != nil || !claims.IsAdmin() {
		t.Fatalf("roles not re-read: %+v %v", claims, err)
	}
}

func TestLogoutIdempotent(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	tok := h.login("alice")

	for i := 0; i < 3; i++ { // repeats succeed
		_, err := h.svc.Logout(context.Background(), &userv1.LogoutRequest{RefreshToken: tok.RefreshToken})
		wantCode(t, err, codes.OK)
	}
	_, err := h.svc.Logout(context.Background(), &userv1.LogoutRequest{RefreshToken: "never-issued"})
	wantCode(t, err, codes.OK)
	_, err = h.svc.Logout(context.Background(), &userv1.LogoutRequest{})
	wantCode(t, err, codes.OK)

	if h.store.activeRefreshCount(alice.ID) != 0 {
		t.Fatal("family not revoked")
	}
	_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
	wantCode(t, err, codes.Unauthenticated)
}

func TestChangePassword(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	admin := h.active("root", RoleAdmin, RoleCustomer)
	tok := h.login("alice")
	const newPW = "a-brand-new-password"

	t.Run("admin is not exempt", func(t *testing.T) {
		_, err := h.svc.ChangePassword(as(admin.ID, RoleAdmin), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: testPassword, NewPassword: newPW})
		wantCode(t, err, codes.PermissionDenied)
	})
	t.Run("other customer denied", func(t *testing.T) {
		_, err := h.svc.ChangePassword(as(admin.ID, RoleCustomer), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: testPassword, NewPassword: newPW})
		wantCode(t, err, codes.PermissionDenied)
	})
	t.Run("bad id", func(t *testing.T) {
		_, err := h.svc.ChangePassword(as(alice.ID), &userv1.ChangePasswordRequest{UserId: "x"})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("weak new password", func(t *testing.T) {
		_, err := h.svc.ChangePassword(as(alice.ID), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: testPassword, NewPassword: "short"})
		wantCode(t, err, codes.InvalidArgument)
		_, err = h.svc.ChangePassword(as(alice.ID), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: testPassword, NewPassword: "alice@example.com"})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("wrong current counts toward lockout", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			_, err := h.svc.ChangePassword(as(alice.ID), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: "wrong-password-123", NewPassword: newPW})
			wantCode(t, err, codes.PermissionDenied)
		}
		if h.store.user(alice.ID).LockedUntil == nil {
			t.Fatal("account not locked")
		}
		h.clock.Advance(16 * time.Minute)
	})
	t.Run("success revokes sessions", func(t *testing.T) {
		_, err := h.svc.ChangePassword(as(alice.ID), &userv1.ChangePasswordRequest{UserId: alice.ID.String(), CurrentPassword: testPassword, NewPassword: newPW})
		wantCode(t, err, codes.OK)
		_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
		wantCode(t, err, codes.Unauthenticated)
		_, err = h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: testPassword})
		wantCode(t, err, codes.Unauthenticated)
		_, err = h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: newPW})
		wantCode(t, err, codes.OK)
	})
}

func TestPasswordResetFlow(t *testing.T) {
	h := newHarness(t)
	alice := h.active("alice")
	tok := h.login("alice")
	const newPW = "reset-password-value"

	reqReset := func() string {
		t.Helper()
		_, err := h.svc.RequestPasswordReset(context.Background(), &userv1.RequestPasswordResetRequest{Email: " ALICE@example.com "})
		wantCode(t, err, codes.OK)
		h.svc.flush()
		raw, ok := h.mailer.last(KindReset, "alice@example.com")
		if !ok {
			t.Fatal("reset mail not sent")
		}
		return raw
	}
	first := reqReset()
	second := reqReset()
	if first == second {
		t.Fatal("tokens should differ")
	}

	t.Run("older reset token invalidated", func(t *testing.T) {
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: first, NewPassword: newPW})
		wantCode(t, err, codes.InvalidArgument)
		if status.Convert(err).Message() != "invalid or expired token" {
			t.Fatalf("message %q", status.Convert(err).Message())
		}
	})
	t.Run("weak password does not burn the token", func(t *testing.T) {
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: second, NewPassword: "short"})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("success clears lockout and sessions", func(t *testing.T) {
		h.store.setUser(alice.ID, func(u *User) { u.FailedLoginCount = 5; l := h.clock.Now().Add(time.Hour); u.LockedUntil = &l })
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: second, NewPassword: newPW})
		wantCode(t, err, codes.OK)
		if u := h.store.user(alice.ID); u.LockedUntil != nil || u.FailedLoginCount != 0 {
			t.Fatalf("lockout not cleared: %+v", u)
		}
		_, err = h.svc.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{RefreshToken: tok.RefreshToken})
		wantCode(t, err, codes.Unauthenticated)
		_, err = h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "alice", Password: newPW})
		wantCode(t, err, codes.OK)
	})
	t.Run("token is single use", func(t *testing.T) {
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: second, NewPassword: newPW})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("unknown and empty token", func(t *testing.T) {
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: "nope", NewPassword: newPW})
		wantCode(t, err, codes.InvalidArgument)
		_, err = h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{NewPassword: newPW})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("expired", func(t *testing.T) {
		tokn := reqReset()
		h.clock.Advance(time.Hour + time.Second)
		_, err := h.svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{ResetToken: tokn, NewPassword: newPW})
		wantCode(t, err, codes.InvalidArgument)
	})
}

func TestRequestPasswordResetIsIndistinguishable(t *testing.T) {
	h := newHarness(t)
	h.active("alice")
	h.seed("banned", userv1.UserStatus_USER_STATUS_SUSPENDED)
	var resps []string
	for _, email := range []string{"alice@example.com", "ghost@example.com", "banned@example.com", "", "not an email", "ALICE@EXAMPLE.COM"} {
		resp, err := h.svc.RequestPasswordReset(context.Background(), &userv1.RequestPasswordResetRequest{Email: email})
		if err != nil {
			t.Fatalf("%q: %v", email, err)
		}
		resps = append(resps, resp.String())
	}
	h.svc.flush()
	for _, r := range resps {
		if r != resps[0] {
			t.Fatalf("responses differ: %v", resps)
		}
	}
	// Mail only goes to the active, existing account.
	if _, ok := h.mailer.last(KindReset, "alice@example.com"); !ok {
		t.Error("alice should get a mail")
	}
	if _, ok := h.mailer.last(KindReset, "banned@example.com"); ok {
		t.Error("suspended user should not get a mail")
	}
	if h.mailer.count() != 2 { // two requests for alice
		t.Errorf("mails sent = %d, want 2", h.mailer.count())
	}
}

func TestVerifyEmail(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.CreateUser(context.Background(), &userv1.CreateUserRequest{Email: "new@example.com", Username: "newbie", Name: "New", Password: testPassword})
	wantCode(t, err, codes.OK)
	h.svc.flush()
	raw, _ := h.mailer.last(KindVerify, "new@example.com")

	t.Run("cannot login before", func(t *testing.T) {
		_, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: "newbie", Password: testPassword})
		wantCode(t, err, codes.FailedPrecondition)
	})
	t.Run("bad token", func(t *testing.T) {
		_, err := h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{VerificationToken: "nope"})
		wantCode(t, err, codes.InvalidArgument)
		_, err = h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("verifies and activates", func(t *testing.T) {
		resp, err := h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{VerificationToken: raw})
		wantCode(t, err, codes.OK)
		if !resp.User.EmailVerified || resp.User.Status != userv1.UserStatus_USER_STATUS_ACTIVE {
			t.Fatalf("user = %+v", resp.User)
		}
		h.login("newbie")
	})
	t.Run("single use", func(t *testing.T) {
		_, err := h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{VerificationToken: raw})
		wantCode(t, err, codes.InvalidArgument)
	})
	t.Run("a reset token is not a verification token", func(t *testing.T) {
		_, _ = h.svc.RequestPasswordReset(context.Background(), &userv1.RequestPasswordResetRequest{Email: "new@example.com"})
		h.svc.flush()
		rt, _ := h.mailer.last(KindReset, "new@example.com")
		_, err := h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{VerificationToken: rt})
		wantCode(t, err, codes.InvalidArgument)
	})
}

func TestVerifyEmailDoesNotUnsuspend(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.CreateUser(context.Background(), &userv1.CreateUserRequest{Email: "sus@example.com", Username: "sus", Name: "S", Password: testPassword})
	wantCode(t, err, codes.OK)
	h.svc.flush()
	raw, _ := h.mailer.last(KindVerify, "sus@example.com")
	u, _ := h.store.GetUserByUsername(context.Background(), "sus")
	h.store.setUser(u.ID, func(u *User) { u.Status = userv1.UserStatus_USER_STATUS_SUSPENDED })

	resp, err := h.svc.VerifyEmail(context.Background(), &userv1.VerifyEmailRequest{VerificationToken: raw})
	wantCode(t, err, codes.OK)
	if resp.User.Status != userv1.UserStatus_USER_STATUS_SUSPENDED || !resp.User.EmailVerified {
		t.Fatalf("user = %+v", resp.User)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	wantErr := func(err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected error")
		}
	}
	wantErr(h.svc.BootstrapAdmin(ctx, "nope", "long-enough-password"))
	wantErr(h.svc.BootstrapAdmin(ctx, "root@example.com", "short"))

	if err := h.svc.BootstrapAdmin(ctx, "Root@Example.com", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	u, err := h.store.GetUserByEmail(ctx, "root@example.com")
	if err != nil || !u.isAdmin() || !u.EmailVerified || u.Status != userv1.UserStatus_USER_STATUS_ACTIVE || len(u.Roles) != 2 {
		t.Fatalf("admin = %+v err %v", u, err)
	}
	// Second call with an admin present is a no-op.
	if err := h.svc.BootstrapAdmin(ctx, "other@example.com", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.GetUserByEmail(ctx, "other@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatal("second admin created")
	}
	_, err = h.svc.Login(ctx, &userv1.LoginRequest{Identifier: "root@example.com", Password: "long-enough-password"})
	wantCode(t, err, codes.OK)
}

func TestMailQueueFullDoesNotBlock(t *testing.T) {
	h := newHarness(t)
	block := make(chan struct{})
	h.svc.enqueue("block", func(context.Context) { <-block })
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < mailQueueSize*3; i++ {
			_, _ = h.svc.RequestPasswordReset(context.Background(), &userv1.RequestPasswordResetRequest{Email: "x@example.com"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RequestPasswordReset blocked on a full queue")
	}
	close(block)
}
