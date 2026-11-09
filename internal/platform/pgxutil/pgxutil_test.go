package pgxutil_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil"
	"github.com/francispowlesland/microservices-app-go/internal/platform/pgxutil/pgxtest"
)

func TestErrorHelpers(t *testing.T) {
	if !pgxutil.IsNotFound(pgx.ErrNoRows) {
		t.Fatal("expected IsNotFound to return true for pgx.ErrNoRows")
	}
	if pgxutil.IsNotFound(errors.New("other error")) {
		t.Fatal("expected IsNotFound to return false for generic error")
	}

	uniqueErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_email_key",
	}
	if !pgxutil.IsUniqueViolation(uniqueErr, "") {
		t.Fatal("expected IsUniqueViolation to return true without constraint filter")
	}
	if !pgxutil.IsUniqueViolation(uniqueErr, "users_email_key") {
		t.Fatal("expected IsUniqueViolation to return true with matching constraint")
	}
	if pgxutil.IsUniqueViolation(uniqueErr, "users_username_key") {
		t.Fatal("expected IsUniqueViolation to return false with non-matching constraint")
	}

	otherPgErr := &pgconn.PgError{Code: "23503"}
	if pgxutil.IsUniqueViolation(otherPgErr, "") {
		t.Fatal("expected IsUniqueViolation to return false for non-23505 code")
	}
}

func TestPostgresIntegration(t *testing.T) {
	dbURL := pgxtest.StartPostgres(t)
	if dbURL == "" {
		return
	}

	ctx := context.Background()

	// Test Migrate with embedded FS
	testFS := fstest.MapFS{
		"001_init.sql": &fstest.MapFile{
			Data: []byte(`-- +goose Up
CREATE TABLE test_items (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE
);
-- +goose Down
DROP TABLE test_items;
`),
		},
	}

	if err := pgxutil.Migrate(ctx, dbURL, testFS, "."); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Test Connect
	pool, err := pgxutil.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer pool.Close()

	// Insert row
	_, err = pool.Exec(ctx, "INSERT INTO test_items (name) VALUES ($1)", "item-1")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Duplicate insert -> unique violation
	_, err = pool.Exec(ctx, "INSERT INTO test_items (name) VALUES ($1)", "item-1")
	if err == nil || !pgxutil.IsUniqueViolation(err, "test_items_name_key") {
		t.Fatalf("expected unique violation on test_items_name_key, got %v", err)
	}

	// Query not found
	var name string
	err = pool.QueryRow(ctx, "SELECT name FROM test_items WHERE id = 9999").Scan(&name)
	if err == nil || !pgxutil.IsNotFound(err) {
		t.Fatalf("expected IsNotFound for missing row, got %v", err)
	}
}
