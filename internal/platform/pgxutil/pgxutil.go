package pgxutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Connect connects to PostgreSQL with sensible pool settings and retries up to 30 seconds
// to handle container startup lag.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgxutil: parse database url: %w", err)
	}
	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 1 * time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	var pool *pgxpool.Pool
	err = retryWithBackoff(ctx, 30*time.Second, func() error {
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pool = p
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pgxutil: connect failed: %w", err)
	}
	return pool, nil
}

// Migrate applies goose migrations from fsys rooted at dir, retrying up to 30 seconds
// to handle container startup lag.
func Migrate(ctx context.Context, databaseURL string, fsys fs.FS, dir string) error {
	err := retryWithBackoff(ctx, 30*time.Second, func() error {
		db, err := sql.Open("pgx", databaseURL)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.PingContext(ctx); err != nil {
			return err
		}

		goose.SetBaseFS(fsys)
		if err := goose.SetDialect("postgres"); err != nil {
			return err
		}
		if err := goose.UpContext(ctx, db, dir); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("pgxutil: migration failed: %w", err)
	}
	return nil
}

// IsUniqueViolation returns true if err is a PostgreSQL unique constraint violation (code 23505).
// If constraint is non-empty, it also verifies that the violation was caused by that constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			if constraint == "" || pgErr.ConstraintName == constraint {
				return true
			}
		}
	}
	return false
}

// IsNotFound returns true if err is pgx.ErrNoRows.
func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func retryWithBackoff(ctx context.Context, maxDuration time.Duration, op func() error) error {
	deadline := time.Now().Add(maxDuration)
	backoff := 100 * time.Millisecond
	const maxBackoff = 2 * time.Second

	var lastErr error
	for {
		if err := op(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
	return fmt.Errorf("timed out after %v, last error: %w", maxDuration, lastErr)
}
