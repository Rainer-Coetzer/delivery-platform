// Package dbtest gives integration tests a fresh, migrated Postgres
// database each, created on the server named by TEST_DATABASE_URL and
// dropped when the test ends.
//
// There is deliberately no testcontainers dependency: point
// TEST_DATABASE_URL at the compose Postgres (or CI's service container),
// which is already a PostGIS image. Without it the tests skip, and they
// only compile under `-tags=integration`, so plain `go test ./...` never
// needs a database.
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable \
//	  go test -tags=integration ./...
package dbtest

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

// New creates a uniquely named database, runs the service's migrations
// against it and returns an open pool. Must be called from a test built
// with the integration tag.
func New(t *testing.T, migrations fs.FS) *db.Pool {
	t.Helper()
	pool, _ := NewDSN(t, migrations)
	return pool
}

// NewDSN is New that also returns the new database's DSN, for components
// that open their own connections (the in-process bus).
func NewDSN(t *testing.T, migrations fs.FS) (*db.Pool, string) {
	t.Helper()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close(ctx)

	name := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Logf("cleanup: connect to drop %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		// FORCE kicks any connection a failed test left open.
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	})

	dsn, err := withDatabase(adminURL, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(dsn, migrations, "."); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool, dsn
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
