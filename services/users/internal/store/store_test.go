package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil/pgxtest"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/service"
	"github.com/francispowlesland/microservices-app-go/services/users/migrations"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := pgxtest.StartPostgres(t) // skips under -short or without Docker
	ctx := context.Background()
	if err := pgxutil.Migrate(ctx, url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxutil.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

func mkUser(name string, at time.Time, roles ...string) service.User {
	if len(roles) == 0 {
		roles = []string{"customer"}
	}
	return service.User{
		ID: uuid.New(), Email: name + "@example.com", Username: name, Name: name, PasswordHash: "hash",
		Status: userv1.UserStatus_USER_STATUS_ACTIVE, Roles: roles, CreatedAt: at, UpdatedAt: at,
	}
}

func mkVerify(u service.User, hash string, at time.Time) service.OneTimeToken {
	return service.OneTimeToken{ID: uuid.New(), UserID: u.ID, Kind: service.KindVerify, Hash: []byte(hash), ExpiresAt: at.Add(24 * time.Hour), CreatedAt: at}
}

func mustCreate(t *testing.T, s *Store, u service.User) {
	t.Helper()
	if err := s.CreateUser(context.Background(), u, mkVerify(u, "v-"+u.Username, u.CreatedAt)); err != nil {
		t.Fatalf("create %s: %v", u.Username, err)
	}
}

func TestUniqueEmailAndUsernameCaseInsensitive(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	mustCreate(t, s, mkUser("alice", base))

	dupEmail := mkUser("other", base)
	dupEmail.Email = "ALICE@Example.com"
	dupName := mkUser("ALICE", base)
	dupName.Email = "different@example.com"

	err := s.CreateUser(ctx, dupEmail, mkVerify(dupEmail, "v1", base))
	if !errors.Is(err, service.ErrEmailTaken) {
		t.Fatalf("dup email err = %v", err)
	}
	err = s.CreateUser(ctx, dupName, mkVerify(dupName, "v2", base))
	if !errors.Is(err, service.ErrUsernameTaken) {
		t.Fatalf("dup username err = %v", err)
	}
	// The failed insert must not leave its verification token behind.
	if _, err := s.GetUser(ctx, dupName.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("user leaked: %v", err)
	}

	// Case-insensitive lookups.
	if u, err := s.GetUserByEmail(ctx, "Alice@EXAMPLE.com"); err != nil || u.Username != "alice" {
		t.Fatalf("by email: %v %v", u, err)
	}
	if _, err := s.GetUserByUsername(ctx, "ALICE"); err != nil {
		t.Fatal(err)
	}

	// Update to a taken username (any case).
	b := mkUser("bob", base.Add(time.Second))
	mustCreate(t, s, b)
	taken := "Alice"
	if _, err := s.UpdateProfile(ctx, b.ID, nil, &taken, base); !errors.Is(err, service.ErrUsernameTaken) {
		t.Fatalf("update clash err = %v", err)
	}
	nm := "Bobby"
	got, err := s.UpdateProfile(ctx, b.ID, &nm, nil, base.Add(time.Minute))
	if err != nil || got.Name != "Bobby" || got.Username != "bob" || !got.UpdatedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("update = %+v %v", got, err)
	}
	if _, err := s.UpdateProfile(ctx, uuid.New(), &nm, nil, base); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestListUsersKeysetOrderingAndBoundaries(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	// Four users share one created_at so the id tie-break is exercised.
	var ids []uuid.UUID
	names := []string{"u1", "u2", "u3", "u4", "u5", "u6"}
	for i, n := range names {
		at := base
		if i >= 4 {
			at = base.Add(time.Second)
		}
		u := mkUser(n, at)
		mustCreate(t, s, u)
		ids = append(ids, u.ID)
	}

	var all []service.User
	var cur *service.ListCursor
	for page := 0; page < 10; page++ {
		rows, err := s.ListUsers(ctx, cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		all = append(all, rows...)
		last := rows[len(rows)-1]
		cur = &service.ListCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(all) != 6 {
		t.Fatalf("got %d users across pages, want 6", len(all))
	}
	seen := map[uuid.UUID]bool{}
	for i, u := range all {
		if seen[u.ID] {
			t.Fatalf("duplicate %s", u.Username)
		}
		seen[u.ID] = true
		if i > 0 {
			p := all[i-1]
			if u.CreatedAt.Before(p.CreatedAt) || (u.CreatedAt.Equal(p.CreatedAt) && u.ID.String() < p.ID.String()) {
				t.Fatalf("order violated at %d: %v then %v", i, p, u)
			}
		}
	}
	// The last two (later created_at) come last.
	if all[4].CreatedAt.Equal(base) || all[5].CreatedAt.Equal(base) {
		t.Fatal("later rows not last")
	}
	// Past the end.
	last := all[5]
	rows, err := s.ListUsers(ctx, &service.ListCursor{CreatedAt: last.CreatedAt, ID: last.ID}, 3)
	if err != nil || len(rows) != 0 {
		t.Fatalf("past end: %v %v", rows, err)
	}
	// limit larger than rows; limit 1.
	if rows, _ := s.ListUsers(ctx, nil, 100); len(rows) != 6 {
		t.Fatalf("limit 100 -> %d", len(rows))
	}
	if rows, _ := s.ListUsers(ctx, nil, 1); len(rows) != 1 {
		t.Fatalf("limit 1 -> %d", len(rows))
	}
	_ = ids
}

func mkRefresh(u service.User, family uuid.UUID, hash string, exp time.Time) service.RefreshToken {
	return service.RefreshToken{ID: uuid.New(), UserID: u.ID, FamilyID: family, Hash: []byte(hash), ExpiresAt: exp, CreatedAt: base}
}

func TestRefreshRotationIsTransactional(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	fam := uuid.New()
	exp := base.Add(time.Hour)
	if err := s.CreateRefreshToken(ctx, mkRefresh(u, fam, "t1", exp)); err != nil {
		t.Fatal(err)
	}
	now := base.Add(time.Minute)

	t.Run("success", func(t *testing.T) {
		got, err := s.RotateRefreshToken(ctx, []byte("t1"), mkRefresh(u, uuid.Nil, "t2", exp), now)
		if err != nil || got.ID != u.ID {
			t.Fatalf("rotate: %v %v", got, err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE family_id=$1 AND revoked_at IS NULL`, fam).Scan(&n)
		if n != 1 {
			t.Fatalf("active in family = %d, want 1", n)
		}
	})
	t.Run("reuse revokes family and persists", func(t *testing.T) {
		_, err := s.RotateRefreshToken(ctx, []byte("t1"), mkRefresh(u, uuid.Nil, "t3", exp), now)
		if !errors.Is(err, service.ErrTokenReused) {
			t.Fatalf("err = %v", err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE family_id=$1 AND revoked_at IS NULL`, fam).Scan(&n)
		if n != 0 {
			t.Fatalf("family not revoked, %d active", n)
		}
		_, err = s.RotateRefreshToken(ctx, []byte("t2"), mkRefresh(u, uuid.Nil, "t4", exp), now)
		if !errors.Is(err, service.ErrTokenReused) {
			t.Fatalf("newest token after reuse: %v", err)
		}
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE token_hash='t3' OR token_hash='t4'`).Scan(&n)
		if n != 0 {
			t.Fatal("rejected rotation inserted a token")
		}
	})
	t.Run("unknown and expired", func(t *testing.T) {
		if _, err := s.RotateRefreshToken(ctx, []byte("nope"), mkRefresh(u, uuid.Nil, "x", exp), now); !errors.Is(err, service.ErrTokenInvalid) {
			t.Fatalf("unknown: %v", err)
		}
		fam2 := uuid.New()
		_ = s.CreateRefreshToken(ctx, mkRefresh(u, fam2, "old", base))
		if _, err := s.RotateRefreshToken(ctx, []byte("old"), mkRefresh(u, uuid.Nil, "y", exp), now); !errors.Is(err, service.ErrTokenInvalid) {
			t.Fatalf("expired: %v", err)
		}
	})
	t.Run("inactive user", func(t *testing.T) {
		_, _ = pool.Exec(ctx, `UPDATE users SET status=3 WHERE id=$1`, u.ID)
		_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "live", exp))
		if _, err := s.RotateRefreshToken(ctx, []byte("live"), mkRefresh(u, uuid.Nil, "z", exp), now); !errors.Is(err, service.ErrUserNotActive) {
			t.Fatalf("suspended: %v", err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE token_hash='z'`).Scan(&n)
		if n != 0 {
			t.Fatal("token inserted for inactive user")
		}
	})
}

func TestConcurrentRotationOnlyOneWins(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "t1", base.Add(time.Hour)))

	var wg sync.WaitGroup
	results := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = s.RotateRefreshToken(ctx, []byte("t1"), mkRefresh(u, uuid.Nil, "n"+string(rune('a'+i)), base.Add(time.Hour)), base.Add(time.Minute))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, service.ErrTokenReused) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d rotations succeeded, want exactly 1", ok)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n)
	if n != 0 {
		// The loser's reuse detection revokes the winner's new token too.
		t.Fatalf("%d tokens remain active after a detected race", n)
	}
}

func TestCascadeDelete(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "t1", base.Add(time.Hour)))
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT count(*) FROM refresh_tokens`, `SELECT count(*) FROM one_time_tokens`, `SELECT count(*) FROM users`} {
		var n int
		_ = pool.QueryRow(ctx, q).Scan(&n)
		if n != 0 {
			t.Fatalf("%s = %d after delete", q, n)
		}
	}
	if err := s.DeleteUser(ctx, u.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("repeat delete: %v", err)
	}
}

func TestLastAdminProtected(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	a1 := mkUser("root1", base, "admin", "customer")
	a2 := mkUser("root2", base.Add(time.Second), "admin", "customer")
	mustCreate(t, s, a1)
	mustCreate(t, s, a2)
	if err := s.DeleteUser(ctx, a1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, a2.ID); !errors.Is(err, service.ErrLastAdmin) {
		t.Fatalf("err = %v", err)
	}
	created, err := s.CreateAdminIfNone(ctx, mkUser("root3", base, "admin"))
	if err != nil || created {
		t.Fatalf("CreateAdminIfNone with admin present: %v %v", created, err)
	}
}

func TestCreateAdminIfNoneConcurrent(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.CreateAdminIfNone(ctx, mkUser("root"+string(rune('a'+i)), base, "admin", "customer"))
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				created++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if created != 1 {
		t.Fatalf("%d admins created, want 1", created)
	}
}

func TestLoginCounters(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	lock := base.Add(15 * time.Minute)
	for i := 1; i <= 5; i++ {
		if err := s.RecordFailedLogin(ctx, u.ID, base, 5, lock); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetUser(ctx, u.ID)
		if got.FailedLoginCount != i {
			t.Fatalf("count = %d, want %d", got.FailedLoginCount, i)
		}
		if (got.LockedUntil != nil) != (i == 5) {
			t.Fatalf("lock at %d: %v", i, got.LockedUntil)
		}
	}
	// After the lock expires, the next failure starts a fresh count.
	if err := s.RecordFailedLogin(ctx, u.ID, lock.Add(time.Second), 5, lock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.FailedLoginCount != 1 || got.LockedUntil != nil {
		t.Fatalf("after expiry: %+v", got)
	}
	if err := s.RecordSuccessfulLogin(ctx, u.ID, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUser(ctx, u.ID)
	if got.FailedLoginCount != 0 || got.LastLoginAt == nil {
		t.Fatalf("after success: %+v", got)
	}
}

func TestOneTimeTokens(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	u.Status = userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION
	mustCreate(t, s, u)
	now := base.Add(time.Minute)

	// Verify: unknown, wrong kind, success, single use.
	if _, err := s.VerifyEmail(ctx, []byte("nope"), now); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("unknown: %v", err)
	}
	got, err := s.VerifyEmail(ctx, []byte("v-alice"), now)
	if err != nil || !got.EmailVerified || got.Status != userv1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("verify: %+v %v", got, err)
	}
	if _, err := s.VerifyEmail(ctx, []byte("v-alice"), now); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("reuse: %v", err)
	}

	// Reset tokens: a new one invalidates earlier unused ones.
	r1 := service.OneTimeToken{ID: uuid.New(), UserID: u.ID, Kind: service.KindReset, Hash: []byte("r1"), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	r2 := service.OneTimeToken{ID: uuid.New(), UserID: u.ID, Kind: service.KindReset, Hash: []byte("r2"), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	_ = s.CreateResetToken(ctx, r1, now)
	_ = s.CreateResetToken(ctx, r2, now)
	if _, err := s.UserByOneTimeToken(ctx, service.KindReset, []byte("r1"), now); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("old reset still valid: %v", err)
	}
	if _, err := s.UserByOneTimeToken(ctx, service.KindVerify, []byte("r2"), now); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("kind mixup: %v", err)
	}
	if _, err := s.UserByOneTimeToken(ctx, service.KindReset, []byte("r2"), now.Add(2*time.Hour)); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("expired: %v", err)
	}
	owner, err := s.UserByOneTimeToken(ctx, service.KindReset, []byte("r2"), now)
	if err != nil || owner.ID != u.ID {
		t.Fatalf("lookup: %v %v", owner, err)
	}

	// ResetPassword: sets hash, revokes sessions, clears lockout, single use.
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "sess", now.Add(time.Hour)))
	_, _ = pool.Exec(ctx, `UPDATE users SET failed_login_count=5, locked_until=$2 WHERE id=$1`, u.ID, now.Add(time.Hour))
	if err := s.ResetPassword(ctx, []byte("r2"), "newhash", now); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetUser(ctx, u.ID)
	if after.PasswordHash != "newhash" || after.LockedUntil != nil || after.FailedLoginCount != 0 {
		t.Fatalf("after reset: %+v", after)
	}
	var active int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&active)
	if active != 0 {
		t.Fatal("sessions survive reset")
	}
	if err := s.ResetPassword(ctx, []byte("r2"), "again", now); !errors.Is(err, service.ErrTokenInvalid) {
		t.Fatalf("reuse: %v", err)
	}
	if after2, _ := s.GetUser(ctx, u.ID); after2.PasswordHash != "newhash" {
		t.Fatal("password changed by invalid token")
	}
}

func TestVerifyEmailKeepsSuspended(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("sus", base)
	u.Status = userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION
	mustCreate(t, s, u)
	_, _ = pool.Exec(ctx, `UPDATE users SET status=3 WHERE id=$1`, u.ID)
	got, err := s.VerifyEmail(ctx, []byte("v-sus"), base)
	if err != nil || got.Status != userv1.UserStatus_USER_STATUS_SUSPENDED || !got.EmailVerified {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestSetPasswordRevokesAllSessions(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "a", base.Add(time.Hour)))
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "b", base.Add(time.Hour)))
	if err := s.SetPassword(ctx, u.ID, "h2", base); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions survive", n)
	}
	if err := s.SetPassword(ctx, uuid.New(), "h", base); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestStatusCheckConstraint(t *testing.T) {
	s, pool := newStore(t)
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status=9 WHERE id=$1`, u.ID); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestRevokeFamilyByHash(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	u := mkUser("alice", base)
	mustCreate(t, s, u)
	fam := uuid.New()
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, fam, "a", base.Add(time.Hour)))
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, fam, "b", base.Add(time.Hour)))
	_ = s.CreateRefreshToken(ctx, mkRefresh(u, uuid.New(), "c", base.Add(time.Hour)))
	if err := s.RevokeFamilyByHash(ctx, []byte("a"), base); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeFamilyByHash(ctx, []byte("unknown"), base); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatalf("active = %d, want 1 (other family)", n)
	}
}
