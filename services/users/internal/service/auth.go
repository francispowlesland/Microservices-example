package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
)

func errInvalidCredentials() error {
	return status.Error(codes.Unauthenticated, "invalid credentials")
}

func errInvalidRefresh() error {
	return status.Error(codes.Unauthenticated, "invalid refresh token")
}

func (s *Service) locked(u User, now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

// issueTokens signs an access token and stores a new refresh token in family.
func (s *Service) issueTokens(ctx context.Context, u User, family uuid.UUID, now time.Time) (*userv1.Tokens, error) {
	access, accessExp, err := s.signer.Sign(u.ID.String(), u.Roles)
	if err != nil {
		return nil, err
	}
	raw, hash, err := newToken()
	if err != nil {
		return nil, err
	}
	rt := RefreshToken{ID: uuid.New(), UserID: u.ID, FamilyID: family, Hash: hash, ExpiresAt: now.Add(s.refreshTTL), CreatedAt: now}
	if err := s.store.CreateRefreshToken(ctx, rt); err != nil {
		return nil, err
	}
	return &userv1.Tokens{
		AccessToken:           access,
		RefreshToken:          raw,
		AccessTokenExpiresAt:  timestamppb.New(accessExp),
		RefreshTokenExpiresAt: timestamppb.New(rt.ExpiresAt),
	}, nil
}

// Login never reveals whether the account exists: unknown users still pay for
// one argon2id verification, and every failure is the same Unauthenticated.
func (s *Service) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	ident := strings.ToLower(strings.TrimSpace(req.GetIdentifier()))
	if ident == "" || req.GetPassword() == "" || len(ident) > maxEmailLen || len(req.GetPassword()) > maxPasswordBytes {
		return nil, errInvalidCredentials()
	}
	var (
		u   User
		err error
	)
	if strings.Contains(ident, "@") {
		u, err = s.store.GetUserByEmail(ctx, ident)
	} else {
		u, err = s.store.GetUserByUsername(ctx, ident)
	}
	if errors.Is(err, ErrNotFound) {
		_, _ = s.hasher.Verify(req.GetPassword(), s.dummyHash)
		return nil, errInvalidCredentials()
	}
	if err != nil {
		return nil, s.internal(ctx, "login lookup", err)
	}

	now := s.nowUTC()
	if s.locked(u, now) {
		return nil, errInvalidCredentials()
	}
	ok, err := s.hasher.Verify(req.GetPassword(), u.PasswordHash)
	if err != nil {
		return nil, s.internal(ctx, "verify password", err)
	}
	if !ok {
		if err := s.store.RecordFailedLogin(ctx, u.ID, now, maxFailedLogins, now.Add(lockDuration)); err != nil {
			return nil, s.internal(ctx, "record failed login", err)
		}
		return nil, errInvalidCredentials()
	}

	switch u.Status {
	case userv1.UserStatus_USER_STATUS_ACTIVE:
	case userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION:
		return nil, status.Error(codes.FailedPrecondition, "email not verified")
	default:
		return nil, status.Error(codes.PermissionDenied, "account suspended")
	}

	if err := s.store.RecordSuccessfulLogin(ctx, u.ID, now); err != nil {
		return nil, s.internal(ctx, "record login", err)
	}
	u.LastLoginAt = &now
	tokens, err := s.issueTokens(ctx, u, uuid.New(), now)
	if err != nil {
		return nil, s.internal(ctx, "issue tokens", err)
	}
	return &userv1.LoginResponse{User: toProto(u), Tokens: tokens}, nil
}

// RefreshToken rotates the presented token. Presenting a revoked token revokes
// its whole family (the store does that and reports ErrTokenReused).
func (s *Service) RefreshToken(ctx context.Context, req *userv1.RefreshTokenRequest) (*userv1.RefreshTokenResponse, error) {
	if req.GetRefreshToken() == "" {
		return nil, errInvalidRefresh()
	}
	now := s.nowUTC()
	raw, hash, err := newToken()
	if err != nil {
		return nil, s.internal(ctx, "new refresh token", err)
	}
	next := RefreshToken{ID: uuid.New(), Hash: hash, ExpiresAt: now.Add(s.refreshTTL), CreatedAt: now}
	u, err := s.store.RotateRefreshToken(ctx, hashToken(req.GetRefreshToken()), next, now)
	switch {
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrTokenReused), errors.Is(err, ErrUserNotActive):
		if errors.Is(err, ErrTokenReused) {
			s.log.WarnContext(ctx, "refresh token reuse detected, family revoked")
		}
		return nil, errInvalidRefresh()
	case err != nil:
		return nil, s.internal(ctx, "rotate refresh token", err)
	}
	access, accessExp, err := s.signer.Sign(u.ID.String(), u.Roles)
	if err != nil {
		return nil, s.internal(ctx, "sign access token", err)
	}
	return &userv1.RefreshTokenResponse{Tokens: &userv1.Tokens{
		AccessToken:           access,
		RefreshToken:          raw,
		AccessTokenExpiresAt:  timestamppb.New(accessExp),
		RefreshTokenExpiresAt: timestamppb.New(next.ExpiresAt),
	}}, nil
}

// Logout is idempotent: unknown or already revoked tokens succeed.
func (s *Service) Logout(ctx context.Context, req *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	if req.GetRefreshToken() != "" {
		if err := s.store.RevokeFamilyByHash(ctx, hashToken(req.GetRefreshToken()), s.nowUTC()); err != nil {
			return nil, s.internal(ctx, "logout", err)
		}
	}
	return &userv1.LogoutResponse{}, nil
}

// ChangePassword requires sub == user_id. Admins are not exempt.
func (s *Service) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (*userv1.ChangePasswordResponse, error) {
	id, err := parseID(req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	}
	if authn.MustFromContext(ctx).Subject != id.String() {
		return nil, status.Error(codes.PermissionDenied, "can only change your own password")
	}
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, s.mapUserErr(ctx, "change password lookup", err)
	}
	now := s.nowUTC()
	denied := status.Error(codes.PermissionDenied, "current password is incorrect")
	if s.locked(u, now) {
		return nil, denied
	}
	ok, err := s.hasher.Verify(req.GetCurrentPassword(), u.PasswordHash)
	if err != nil {
		return nil, s.internal(ctx, "verify password", err)
	}
	if !ok {
		if err := s.store.RecordFailedLogin(ctx, u.ID, now, maxFailedLogins, now.Add(lockDuration)); err != nil {
			return nil, s.internal(ctx, "record failed login", err)
		}
		return nil, denied
	}
	if err := checkPassword(req.GetNewPassword(), u.Email, u.Username); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hash, err := s.hasher.Hash(req.GetNewPassword())
	if err != nil {
		return nil, s.internal(ctx, "hash password", err)
	}
	if err := s.store.SetPassword(ctx, u.ID, hash, now); err != nil {
		return nil, s.mapUserErr(ctx, "set password", err)
	}
	return &userv1.ChangePasswordResponse{}, nil
}

// RequestPasswordReset always returns the same empty success. All work that
// differs between known and unknown emails (lookup, token write, mail) runs on
// the background worker, so the response and its latency do not depend on it.
func (s *Service) RequestPasswordReset(_ context.Context, req *userv1.RequestPasswordResetRequest) (*userv1.RequestPasswordResetResponse, error) {
	email := strings.ToLower(strings.TrimSpace(req.GetEmail()))
	if email != "" && len(email) <= maxEmailLen {
		s.enqueue("password reset", func(ctx context.Context) { s.processReset(ctx, email) })
	}
	return &userv1.RequestPasswordResetResponse{}, nil
}

func (s *Service) processReset(ctx context.Context, email string) {
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("password reset lookup failed", slog.Any("error", err))
		}
		return
	}
	if u.Status == userv1.UserStatus_USER_STATUS_SUSPENDED {
		return
	}
	raw, hash, err := newToken()
	if err != nil {
		s.log.Error("password reset token failed", slog.Any("error", err))
		return
	}
	now := s.nowUTC()
	t := OneTimeToken{ID: uuid.New(), UserID: u.ID, Kind: KindReset, Hash: hash, ExpiresAt: now.Add(resetTokenTTL), CreatedAt: now}
	if err := s.store.CreateResetToken(ctx, t, now); err != nil {
		s.log.Error("password reset store failed", slog.Any("error", err))
		return
	}
	if err := s.mailer.SendPasswordReset(u.Email, raw); err != nil {
		s.log.Error("password reset mail failed", slog.Any("error", err))
	}
}

func (s *Service) ResetPassword(ctx context.Context, req *userv1.ResetPasswordRequest) (*userv1.ResetPasswordResponse, error) {
	bad := status.Error(codes.InvalidArgument, "invalid or expired token")
	if req.GetResetToken() == "" {
		return nil, bad
	}
	hash := hashToken(req.GetResetToken())
	now := s.nowUTC()
	u, err := s.store.UserByOneTimeToken(ctx, KindReset, hash, now)
	if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrNotFound) {
		return nil, bad
	}
	if err != nil {
		return nil, s.internal(ctx, "reset lookup", err)
	}
	if err := checkPassword(req.GetNewPassword(), u.Email, u.Username); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	pwHash, err := s.hasher.Hash(req.GetNewPassword())
	if err != nil {
		return nil, s.internal(ctx, "hash password", err)
	}
	if err := s.store.ResetPassword(ctx, hash, pwHash, now); err != nil {
		if errors.Is(err, ErrTokenInvalid) {
			return nil, bad
		}
		return nil, s.internal(ctx, "reset password", err)
	}
	return &userv1.ResetPasswordResponse{}, nil
}

func (s *Service) VerifyEmail(ctx context.Context, req *userv1.VerifyEmailRequest) (*userv1.VerifyEmailResponse, error) {
	bad := status.Error(codes.InvalidArgument, "invalid or expired token")
	if req.GetVerificationToken() == "" {
		return nil, bad
	}
	u, err := s.store.VerifyEmail(ctx, hashToken(req.GetVerificationToken()), s.nowUTC())
	if errors.Is(err, ErrTokenInvalid) || errors.Is(err, ErrNotFound) {
		return nil, bad
	}
	if err != nil {
		return nil, s.internal(ctx, "verify email", err)
	}
	return &userv1.VerifyEmailResponse{User: toProto(u)}, nil
}

// BootstrapAdmin creates the first admin if none exists. It logs only that an
// admin was created, never the credentials.
func (s *Service) BootstrapAdmin(ctx context.Context, email, pw string) error {
	email, err := normaliseEmail(email)
	if err != nil {
		return err
	}
	const username = "admin"
	if err := checkPassword(pw, email, username); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(pw)
	if err != nil {
		return err
	}
	now := s.nowUTC()
	created, err := s.store.CreateAdminIfNone(ctx, User{
		ID: uuid.New(), Email: email, Username: username, Name: "Administrator",
		PasswordHash: hash, Status: userv1.UserStatus_USER_STATUS_ACTIVE, EmailVerified: true,
		Roles: []string{RoleAdmin, RoleCustomer}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if created {
		s.log.Info("bootstrap admin created")
	}
	return nil
}
