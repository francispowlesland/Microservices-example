package service

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
)

// fakeStore is an in-memory Store mirroring the Postgres semantics.
type fakeStore struct {
	mu      sync.Mutex
	users   map[uuid.UUID]*User
	refresh []*fakeRefresh
	ott     []*fakeOTT
	// failNext makes the next call to any method return this error.
	failNext error
}

type fakeRefresh struct {
	RefreshToken
	revokedAt *time.Time
}

type fakeOTT struct {
	OneTimeToken
	usedAt *time.Time
}

func newFakeStore() *fakeStore { return &fakeStore{users: map[uuid.UUID]*User{}} }

func (f *fakeStore) fail() error {
	err := f.failNext
	f.failNext = nil
	return err
}

func (f *fakeStore) clash(email, username string, except uuid.UUID) error {
	for id, u := range f.users {
		if id == except {
			continue
		}
		if strings.EqualFold(u.Email, email) {
			return ErrEmailTaken
		}
		if strings.EqualFold(u.Username, username) {
			return ErrUsernameTaken
		}
	}
	return nil
}

func (f *fakeStore) CreateUser(_ context.Context, u User, v OneTimeToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return err
	}
	if err := f.clash(u.Email, u.Username, uuid.Nil); err != nil {
		return err
	}
	cp := u
	f.users[u.ID] = &cp
	f.ott = append(f.ott, &fakeOTT{OneTimeToken: v})
	return nil
}

func (f *fakeStore) CreateAdminIfNone(_ context.Context, u User) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.users {
		if e.isAdmin() {
			return false, nil
		}
	}
	if err := f.clash(u.Email, u.Username, uuid.Nil); err != nil {
		return false, err
	}
	cp := u
	f.users[u.ID] = &cp
	return true, nil
}

func (f *fakeStore) get(pred func(*User) bool) (User, error) {
	if err := f.fail(); err != nil {
		return User{}, err
	}
	for _, u := range f.users {
		if pred(u) {
			return *u, nil
		}
	}
	return User{}, ErrNotFound
}

func (f *fakeStore) GetUser(_ context.Context, id uuid.UUID) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.get(func(u *User) bool { return u.ID == id })
}

func (f *fakeStore) GetUserByEmail(_ context.Context, e string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.get(func(u *User) bool { return strings.EqualFold(u.Email, e) })
}

func (f *fakeStore) GetUserByUsername(_ context.Context, n string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.get(func(u *User) bool { return strings.EqualFold(u.Username, n) })
}

func (f *fakeStore) UpdateProfile(_ context.Context, id uuid.UUID, name, username *string, now time.Time) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if username != nil {
		if err := f.clash("\x00", *username, id); err != nil {
			return User{}, err
		}
		u.Username = *username
	}
	if name != nil {
		u.Name = *name
	}
	u.UpdatedAt = now
	return *u, nil
}

func (f *fakeStore) DeleteUser(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return ErrNotFound
	}
	if u.isAdmin() {
		n := 0
		for _, o := range f.users {
			if o.isAdmin() {
				n++
			}
		}
		if n == 1 {
			return ErrLastAdmin
		}
	}
	delete(f.users, id)
	var keep []*fakeRefresh
	for _, r := range f.refresh {
		if r.UserID != id {
			keep = append(keep, r)
		}
	}
	f.refresh = keep
	return nil
}

func (f *fakeStore) ListUsers(_ context.Context, cur *ListCursor, limit int) ([]User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := make([]User, 0, len(f.users))
	for _, u := range f.users {
		all = append(all, *u)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID.String() < all[j].ID.String()
	})
	var out []User
	for _, u := range all {
		if cur != nil {
			if u.CreatedAt.Before(cur.CreatedAt) || (u.CreatedAt.Equal(cur.CreatedAt) && u.ID.String() <= cur.ID.String()) {
				continue
			}
		}
		out = append(out, u)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) RecordFailedLogin(_ context.Context, id uuid.UUID, now time.Time, max int, lockUntil time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[id]
	if u.LockedUntil != nil && !u.LockedUntil.After(now) {
		u.FailedLoginCount, u.LockedUntil = 0, nil
	}
	u.FailedLoginCount++
	if u.FailedLoginCount >= max {
		u.LockedUntil = &lockUntil
	}
	return nil
}

func (f *fakeStore) RecordSuccessfulLogin(_ context.Context, id uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := f.users[id]
	u.FailedLoginCount, u.LockedUntil, u.LastLoginAt = 0, nil, &now
	return nil
}

func (f *fakeStore) CreateRefreshToken(_ context.Context, t RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh = append(f.refresh, &fakeRefresh{RefreshToken: t})
	return nil
}

func (f *fakeStore) findRefresh(h []byte) *fakeRefresh {
	for _, r := range f.refresh {
		if bytes.Equal(r.Hash, h) {
			return r
		}
	}
	return nil
}

func (f *fakeStore) revokeFamily(fam uuid.UUID, now time.Time) {
	for _, r := range f.refresh {
		if r.FamilyID == fam && r.revokedAt == nil {
			t := now
			r.revokedAt = &t
		}
	}
}

func (f *fakeStore) RotateRefreshToken(_ context.Context, old []byte, next RefreshToken, now time.Time) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.findRefresh(old)
	if r == nil {
		return User{}, ErrTokenInvalid
	}
	if r.revokedAt != nil {
		f.revokeFamily(r.FamilyID, now)
		return User{}, ErrTokenReused
	}
	if !now.Before(r.ExpiresAt) {
		return User{}, ErrTokenInvalid
	}
	u := f.users[r.UserID]
	if u.Status != userv1.UserStatus_USER_STATUS_ACTIVE {
		return User{}, ErrUserNotActive
	}
	t := now
	r.revokedAt = &t
	next.UserID, next.FamilyID = r.UserID, r.FamilyID
	f.refresh = append(f.refresh, &fakeRefresh{RefreshToken: next})
	return *u, nil
}

func (f *fakeStore) RevokeFamilyByHash(_ context.Context, h []byte, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.findRefresh(h); r != nil {
		f.revokeFamily(r.FamilyID, now)
	}
	return nil
}

func (f *fakeStore) revokeAllFor(id uuid.UUID, now time.Time) {
	for _, r := range f.refresh {
		if r.UserID == id && r.revokedAt == nil {
			t := now
			r.revokedAt = &t
		}
	}
}

func (f *fakeStore) SetPassword(_ context.Context, id uuid.UUID, hash string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return ErrNotFound
	}
	u.PasswordHash, u.FailedLoginCount, u.LockedUntil = hash, 0, nil
	f.revokeAllFor(id, now)
	return nil
}

func (f *fakeStore) CreateResetToken(_ context.Context, t OneTimeToken, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.ott {
		if o.UserID == t.UserID && o.Kind == KindReset && o.usedAt == nil {
			n := now
			o.usedAt = &n
		}
	}
	f.ott = append(f.ott, &fakeOTT{OneTimeToken: t})
	return nil
}

func (f *fakeStore) validOTT(kind string, h []byte, now time.Time) *fakeOTT {
	for _, o := range f.ott {
		if o.Kind == kind && bytes.Equal(o.Hash, h) && o.usedAt == nil && o.ExpiresAt.After(now) {
			return o
		}
	}
	return nil
}

func (f *fakeStore) UserByOneTimeToken(_ context.Context, kind string, h []byte, now time.Time) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.validOTT(kind, h, now)
	if o == nil {
		return User{}, ErrTokenInvalid
	}
	return *f.users[o.UserID], nil
}

func (f *fakeStore) ResetPassword(_ context.Context, h []byte, pw string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.validOTT(KindReset, h, now)
	if o == nil {
		return ErrTokenInvalid
	}
	o.usedAt = &now
	u := f.users[o.UserID]
	u.PasswordHash, u.FailedLoginCount, u.LockedUntil = pw, 0, nil
	f.revokeAllFor(u.ID, now)
	return nil
}

func (f *fakeStore) VerifyEmail(_ context.Context, h []byte, now time.Time) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.validOTT(KindVerify, h, now)
	if o == nil {
		return User{}, ErrTokenInvalid
	}
	o.usedAt = &now
	u := f.users[o.UserID]
	u.EmailVerified = true
	if u.Status == userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION {
		u.Status = userv1.UserStatus_USER_STATUS_ACTIVE
	}
	u.UpdatedAt = now
	return *u, nil
}

// ---- test-only inspection helpers ----

func (f *fakeStore) activeRefreshCount(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.refresh {
		if r.UserID == id && r.revokedAt == nil {
			n++
		}
	}
	return n
}

func (f *fakeStore) setUser(id uuid.UUID, fn func(*User)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.users[id])
}

func (f *fakeStore) user(id uuid.UUID) User {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.users[id]
}
