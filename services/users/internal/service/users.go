package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pagination"
)

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, "id must be a valid UUID")
	}
	return id, nil
}

// authorizeSelfOrAdmin returns NotFound (not PermissionDenied) for other
// people's rows so IDs cannot be probed.
func authorizeSelfOrAdmin(ctx context.Context, id uuid.UUID) error {
	c := authn.MustFromContext(ctx)
	if c.IsAdmin() || c.Subject == id.String() {
		return nil
	}
	return status.Error(codes.NotFound, "user not found")
}

// CreateUser is public self-signup. Roles and status are fixed by the server.
func (s *Service) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	email, err := normaliseEmail(req.GetEmail())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	username, err := normaliseUsername(req.GetUsername())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	name, err := normaliseName(req.GetName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := checkPassword(req.GetPassword(), email, username); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hash, err := s.hasher.Hash(req.GetPassword())
	if err != nil {
		return nil, s.internal(ctx, "hash password", err)
	}
	raw, tokHash, err := newToken()
	if err != nil {
		return nil, s.internal(ctx, "new verification token", err)
	}

	now := s.nowUTC()
	u := User{
		ID: uuid.New(), Email: email, Username: username, Name: name,
		PasswordHash: hash, Status: userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION,
		Roles: []string{RoleCustomer}, CreatedAt: now, UpdatedAt: now,
	}
	vt := OneTimeToken{ID: uuid.New(), UserID: u.ID, Kind: KindVerify, Hash: tokHash, ExpiresAt: now.Add(verifyTokenTTL), CreatedAt: now}
	if err := s.store.CreateUser(ctx, u, vt); err != nil {
		switch {
		case errors.Is(err, ErrEmailTaken):
			return nil, status.Error(codes.AlreadyExists, "email already registered")
		case errors.Is(err, ErrUsernameTaken):
			return nil, status.Error(codes.AlreadyExists, "username already taken")
		}
		return nil, s.internal(ctx, "create user", err)
	}
	s.enqueue("send verification", func(context.Context) {
		if err := s.mailer.SendVerification(email, raw); err != nil {
			s.log.Error("send verification failed", "error", err)
		}
	})
	return &userv1.CreateUserResponse{User: toProto(u)}, nil
}

func (s *Service) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := authorizeSelfOrAdmin(ctx, id); err != nil {
		return nil, err
	}
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, s.mapUserErr(ctx, "get user", err)
	}
	return &userv1.GetUserResponse{User: toProto(u)}, nil
}

func (s *Service) mapUserErr(ctx context.Context, op string, err error) error {
	if errors.Is(err, ErrNotFound) {
		return status.Error(codes.NotFound, "user not found")
	}
	return s.internal(ctx, op, err)
}

// UpdateUser applies only the fields named in update_mask (name, username).
func (s *Service) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	if req.GetUser() == nil {
		return nil, status.Error(codes.InvalidArgument, "user is required")
	}
	id, err := parseID(req.GetUser().GetId())
	if err != nil {
		return nil, err
	}
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, status.Error(codes.InvalidArgument, "update_mask is required and must be non-empty")
	}
	var name, username *string
	for _, p := range paths {
		switch p {
		case "name":
			n, err := normaliseName(req.GetUser().GetName())
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			name = &n
		case "username":
			u, err := normaliseUsername(req.GetUser().GetUsername())
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			username = &u
		default:
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("update_mask path %q is not updatable (allowed: name, username)", p))
		}
	}
	if err := authorizeSelfOrAdmin(ctx, id); err != nil {
		return nil, err
	}
	u, err := s.store.UpdateProfile(ctx, id, name, username, s.nowUTC())
	if err != nil {
		if errors.Is(err, ErrUsernameTaken) {
			return nil, status.Error(codes.AlreadyExists, "username already taken")
		}
		return nil, s.mapUserErr(ctx, "update user", err)
	}
	return &userv1.UpdateUserResponse{User: toProto(u)}, nil
}

func (s *Service) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := authorizeSelfOrAdmin(ctx, id); err != nil {
		return nil, err
	}
	if err := s.store.DeleteUser(ctx, id); err != nil {
		if errors.Is(err, ErrLastAdmin) {
			return nil, status.Error(codes.FailedPrecondition, "cannot delete the last admin")
		}
		return nil, s.mapUserErr(ctx, "delete user", err)
	}
	return &userv1.DeleteUserResponse{}, nil
}

// ListUsers is admin only. The page token carries only the keyset position.
func (s *Service) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	if !authn.MustFromContext(ctx).IsAdmin() {
		return nil, status.Error(codes.PermissionDenied, "admin only")
	}
	var cur *ListCursor
	if req.GetPageToken() != "" {
		at, id, err := pagination.Decode(req.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		cur = &ListCursor{CreatedAt: at, ID: id}
	}
	size := pagination.PageSize(req.GetPageSize())
	users, err := s.store.ListUsers(ctx, cur, size+1)
	if err != nil {
		return nil, s.internal(ctx, "list users", err)
	}
	resp := &userv1.ListUsersResponse{}
	if len(users) > size {
		users = users[:size]
		last := users[size-1]
		resp.NextPageToken = pagination.Encode(last.CreatedAt, last.ID)
	}
	for _, u := range users {
		resp.Users = append(resp.Users, toProto(u))
	}
	return resp, nil
}
