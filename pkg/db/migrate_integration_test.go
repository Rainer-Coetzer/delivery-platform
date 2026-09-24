//go:build integration

package db_test

import (
	"context"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
)

// A migration slow enough that two boots overlap: without the advisory
// lock the second one fails on "relation already exists" or on goose's
// version table.
var slowMigrations = fstest.MapFS{
	"00001_init.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (id INT PRIMARY KEY);
SELECT pg_sleep(1);
-- +goose Down
DROP TABLE widgets;
`)},
	"00002_more.sql": {Data: []byte(`-- +goose Up
ALTER TABLE widgets ADD COLUMN name TEXT;
-- +goose Down
ALTER TABLE widgets DROP COLUMN name;
`)},
}

// Two platform replicas booting at once: both succeed, the schema is
// migrated exactly once, and a third (later) boot is a no-op.
func TestMigrateSchema_ConcurrentBootsAndRestart(t *testing.T) {
	_, dsn := dbtest.NewDSN(t, fstest.MapFS{})
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = db.MigrateSchema(ctx, dsn, "widgets_mod", slowMigrations)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d: %v", i, err)
		}
	}
	// Restart: must find its version table and do nothing (this was the
	// quoted-table-name bug: the second boot tried to recreate it).
	if err := db.MigrateSchema(ctx, dsn, "widgets_mod", slowMigrations); err != nil {
		t.Fatalf("restart: %v", err)
	}

	pool, err := db.OpenSchema(ctx, dsn, "widgets_mod", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM widgets_mod.goose_db_version WHERE version_id > 0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("applied %d migration rows, want exactly 2", n)
	}
	// Unqualified name resolves through search_path to the module schema.
	if _, err := pool.Exec(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'x')`); err != nil {
		t.Fatalf("unqualified insert via search_path: %v", err)
	}
}
