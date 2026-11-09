// Package store is the pgx implementation of service.Store. All SQL lives here.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	userv1 "github.com/francispowlesland/microservices-app-go/gen/go/user/v1"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/services/users/internal/service"
)

// Constraint names from the migration.
const (
	emailIndex    = "users_email_lower_key"
	usernameIndex = "users_username_lower_key"
)

// Store implements service.Store on PostgreSQL.
type Store struct{ pool *pgxpool.Pool }

var _ service.Store = (*Store)(nil)

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const userCols = `id, email, username, name, password_hash, status, email_verified, roles,
	created_at, updated_at, last_login_at, failed_login_count, locked_until`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(r rowScanner) (service.User, error) {
	var (
		u      service.User
		status int16
	)
	err := r.Scan(&u.ID, &u.Email, &u.Username, &u.Name, &u.PasswordHash, &status, &u.EmailVerified, &u.Roles,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &u.FailedLoginCount, &u.LockedUntil)
	if err != nil {
		if pgxutil.IsNotFound(err) {
			return service.User{}, service.ErrNotFound
		}
		return service.User{}, err
	}
	u.Status = userv1.UserStatus(status)
	return u, nil
}

func mapUnique(err error) error {
	switch {
	case pgxutil.IsUniqueViolation(err, emailIndex):
		return service.ErrEmailTaken
	case pgxutil.IsUniqueViolation(err, usernameIndex):
		return service.ErrUsernameTaken
	}
	return err
}

func insertUser(ctx context.Context, q pgx.Tx, u service.User) error {
	_, err := q.Exec(ctx, `INSERT INTO users (`+userCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		u.ID, u.Email, u.Username, u.Name, u.PasswordHash, int16(u.Status), u.EmailVerified, u.Roles,
		u.CreatedAt, u.UpdatedAt, u.LastLoginAt, u.FailedLoginCount, u.LockedUntil)
	return mapUnique(err)
}

func (s *Store) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}

func (s *Store) CreateUser(ctx context.Context, u service.User, verify service.OneTimeToken) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := insertUser(ctx, tx, u); err != nil {
			return err
		}
		return insertOneTime(ctx, tx, verify)
	})
}

func insertOneTime(ctx context.Context, tx pgx.Tx, t service.OneTimeToken) error {
	_, err := tx.Exec(ctx, `INSERT INTO one_time_tokens (id, user_id, kind, token_hash, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.ID, t.UserID, t.Kind, t.Hash, t.ExpiresAt, t.CreatedAt)
	return err
}

func (s *Store) CreateAdminIfNone(ctx context.Context, u service.User) (bool, error) {
	created := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// Serialise concurrent bootstraps (e.g. several replicas starting together).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('users.bootstrap_admin'))`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE 'admin' = ANY(roles))`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if err := insertUser(ctx, tx, u); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created && err == nil, err
}

func (s *Store) getBy(ctx context.Context, where string, arg any) (service.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE `+where, arg))
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (service.User, error) {
	return s.getBy(ctx, `id = $1`, id)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (service.User, error) {
	return s.getBy(ctx, `lower(email) = lower($1)`, email)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (service.User, error) {
	return s.getBy(ctx, `lower(username) = lower($1)`, username)
}

func (s *Store) UpdateProfile(ctx context.Context, id uuid.UUID, name, username *string, now time.Time) (service.User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `UPDATE users SET
			name = COALESCE($2, name), username = COALESCE($3, username), updated_at = $4
		WHERE id = $1 RETURNING `+userCols, id, name, username, now))
	if err != nil {
		return service.User{}, mapUnique(err)
	}
	return u, nil
}

func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// Lock all admin rows so two concurrent deletes of the last two admins cannot both pass.
		rows, err := tx.Query(ctx, `SELECT id FROM users WHERE 'admin' = ANY(roles) FOR UPDATE`)
		if err != nil {
			return err
		}
		admins, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if len(admins) == 1 && admins[0] == id {
			return service.ErrLastAdmin
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return service.ErrNotFound
		}
		return nil
	})
}

func (s *Store) ListUsers(ctx context.Context, cur *service.ListCursor, limit int) ([]service.User, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if cur == nil {
		rows, err = s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at, id LIMIT $1`, limit)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT `+userCols+` FROM users
			WHERE (created_at, id) > ($1, $2) ORDER BY created_at, id LIMIT $3`, cur.CreatedAt, cur.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) RecordFailedLogin(ctx context.Context, id uuid.UUID, now time.Time, maxFailures int, lockUntil time.Time) error {
	// A lock that has expired restarts the count so one stale failure
	// cannot re-lock the account on the next typo.
	_, err := s.pool.Exec(ctx, `UPDATE users SET
			failed_login_count = CASE WHEN locked_until IS NOT NULL AND locked_until <= $2 THEN 1 ELSE failed_login_count + 1 END,
			locked_until = CASE
				WHEN (CASE WHEN locked_until IS NOT NULL AND locked_until <= $2 THEN 1 ELSE failed_login_count + 1 END) >= $3
				THEN $4 ELSE CASE WHEN locked_until IS NOT NULL AND locked_until <= $2 THEN NULL ELSE locked_until END END
		WHERE id = $1`, id, now, maxFailures, lockUntil)
	return err
}

func (s *Store) RecordSuccessfulLogin(ctx context.Context, id uuid.UUID, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET failed_login_count = 0, locked_until = NULL, last_login_at = $2 WHERE id = $1`, id, now)
	return err
}

func (s *Store) CreateRefreshToken(ctx context.Context, t service.RefreshToken) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.ID, t.UserID, t.FamilyID, t.Hash, t.ExpiresAt, t.CreatedAt)
	return err
}

// RotateRefreshToken revokes the old token and inserts next in one transaction.
// Reuse of a revoked token revokes the family; that write must survive, so the
// transaction commits and the sentinel error is returned afterwards.
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash []byte, next service.RefreshToken, now time.Time) (service.User, error) {
	var (
		user     service.User
		sentinel error
	)
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var (
			userID, familyID uuid.UUID
			expiresAt        time.Time
			revokedAt        *time.Time
		)
		err := tx.QueryRow(ctx, `SELECT user_id, family_id, expires_at, revoked_at FROM refresh_tokens
			WHERE token_hash = $1 FOR UPDATE`, oldHash).Scan(&userID, &familyID, &expiresAt, &revokedAt)
		if pgxutil.IsNotFound(err) {
			sentinel = service.ErrTokenInvalid
			return nil
		}
		if err != nil {
			return err
		}
		if revokedAt != nil {
			if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, now); err != nil {
				return err
			}
			sentinel = service.ErrTokenReused
			return nil
		}
		if !now.Before(expiresAt) {
			sentinel = service.ErrTokenInvalid
			return nil
		}
		u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, userID))
		if err != nil {
			return err
		}
		if u.Status != userv1.UserStatus_USER_STATUS_ACTIVE {
			sentinel = service.ErrUserNotActive
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE token_hash = $1`, oldHash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, next.ID, userID, familyID, next.Hash, next.ExpiresAt, next.CreatedAt); err != nil {
			return err
		}
		user = u
		return nil
	})
	if err != nil {
		return service.User{}, err
	}
	if sentinel != nil {
		return service.User{}, sentinel
	}
	return user, nil
}

func (s *Store) RevokeFamilyByHash(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2
		WHERE revoked_at IS NULL AND family_id IN (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)`, hash, now)
	return err
}

func revokeAll(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now)
	return err
}

func (s *Store) SetPassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = $3, failed_login_count = 0, locked_until = NULL WHERE id = $1`, id, hash, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return service.ErrNotFound
		}
		return revokeAll(ctx, tx, id, now)
	})
}

func (s *Store) CreateResetToken(ctx context.Context, t service.OneTimeToken, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE one_time_tokens SET used_at = $3 WHERE user_id = $1 AND kind = $2 AND used_at IS NULL`, t.UserID, service.KindReset, now); err != nil {
			return err
		}
		return insertOneTime(ctx, tx, t)
	})
}

func (s *Store) UserByOneTimeToken(ctx context.Context, kind string, hash []byte, now time.Time) (service.User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+prefixed("u.", userCols)+` FROM one_time_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.kind = $1 AND t.token_hash = $2 AND t.used_at IS NULL AND t.expires_at > $3`, kind, hash, now))
	if errors.Is(err, service.ErrNotFound) {
		return service.User{}, service.ErrTokenInvalid
	}
	return u, err
}

// consumeToken marks a valid token used and returns its owner id.
func consumeToken(ctx context.Context, tx pgx.Tx, kind string, hash []byte, now time.Time) (uuid.UUID, error) {
	var uid uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE one_time_tokens SET used_at = $3
		WHERE kind = $1 AND token_hash = $2 AND used_at IS NULL AND expires_at > $3 RETURNING user_id`, kind, hash, now).Scan(&uid)
	if pgxutil.IsNotFound(err) {
		return uuid.Nil, service.ErrTokenInvalid
	}
	return uid, err
}

func (s *Store) ResetPassword(ctx context.Context, hash []byte, newPasswordHash string, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		uid, err := consumeToken(ctx, tx, service.KindReset, hash, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = $3, failed_login_count = 0, locked_until = NULL WHERE id = $1`, uid, newPasswordHash, now); err != nil {
			return err
		}
		return revokeAll(ctx, tx, uid, now)
	})
}

func (s *Store) VerifyEmail(ctx context.Context, hash []byte, now time.Time) (service.User, error) {
	var out service.User
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		uid, err := consumeToken(ctx, tx, service.KindVerify, hash, now)
		if err != nil {
			return err
		}
		out, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET
				email_verified = true,
				status = CASE WHEN status = $2 THEN $3 ELSE status END,
				updated_at = $4
			WHERE id = $1 RETURNING `+userCols,
			uid, int16(userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION), int16(userv1.UserStatus_USER_STATUS_ACTIVE), now))
		return err
	})
	if err != nil {
		return service.User{}, fmt.Errorf("verify email: %w", err)
	}
	return out, nil
}

// prefixed qualifies a comma-separated column list with a table alias.
func prefixed(alias, cols string) string {
	out := ""
	start := 0
	for i := 0; i <= len(cols); i++ {
		if i == len(cols) || cols[i] == ',' {
			col := cols[start:i]
			for len(col) > 0 && (col[0] == ' ' || col[0] == '\n' || col[0] == '\t') {
				col = col[1:]
			}
			if out != "" {
				out += ", "
			}
			out += alias + col
			start = i + 1
		}
	}
	return out
}
