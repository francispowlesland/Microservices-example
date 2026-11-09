package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/authn"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/password"
)

const testPassword = "correct-horse-battery"

type sentMail struct{ to, token, kind string }

type recMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *recMailer) SendVerification(to, tok string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to, tok, KindVerify})
	return nil
}

func (m *recMailer) SendPasswordReset(to, tok string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{to, tok, KindReset})
	return nil
}

// last returns the most recent mail of kind to the address.
func (m *recMailer) last(kind, to string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if m.sent[i].kind == kind && m.sent[i].to == to {
			return m.sent[i].token, true
		}
	}
	return "", false
}

func (m *recMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	t      *testing.T
	svc    *Service
	store  *fakeStore
	mailer *recMailer
	clock  *clock
	verify *authn.Verifier
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, store: newFakeStore(), mailer: &recMailer{},
		clock:  &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		verify: authn.NewVerifier(pub, authn.Issuer, authn.Audience),
	}
	svc, err := New(Options{
		Store: h.store, Mailer: h.mailer,
		Signer:     authn.NewSigner(priv, authn.Issuer, authn.Audience, 15*time.Minute),
		Hasher:     password.NewHasher(password.Cheap),
		RefreshTTL: time.Hour,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        h.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	t.Cleanup(svc.Close)
	return h
}

// as returns a context carrying claims, as the authn interceptor would.
func as(sub uuid.UUID, roles ...string) context.Context {
	return authn.NewContext(context.Background(), authn.Claims{Subject: sub.String(), Roles: roles})
}

// seed inserts a user directly with testPassword.
func (h *harness) seed(username string, st userv1.UserStatus, roles ...string) User {
	h.t.Helper()
	hash, err := h.svc.hasher.Hash(testPassword)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(roles) == 0 {
		roles = []string{RoleCustomer}
	}
	now := h.clock.Now()
	u := User{
		ID: uuid.New(), Email: username + "@example.com", Username: username, Name: "Name " + username,
		PasswordHash: hash, Status: st, EmailVerified: st != userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION,
		Roles: roles, CreatedAt: now, UpdatedAt: now,
	}
	h.clock.Advance(time.Second) // distinct created_at for ordering
	h.store.mu.Lock()
	h.store.users[u.ID] = &u
	h.store.mu.Unlock()
	return u
}

func (h *harness) active(username string, roles ...string) User {
	return h.seed(username, userv1.UserStatus_USER_STATUS_ACTIVE, roles...)
}

func (h *harness) login(username string) *userv1.Tokens {
	h.t.Helper()
	resp, err := h.svc.Login(context.Background(), &userv1.LoginRequest{Identifier: username, Password: testPassword})
	if err != nil {
		h.t.Fatalf("login %s: %v", username, err)
	}
	return resp.Tokens
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}
