package db

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io/fs"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migrate applies every .sql migration in fsys (embedded, at its root) to
// the database at dsn, in the default schema. Used by the per-service
// deployment and by tests; the platform binary uses MigrateSchema.
func Migrate(dsn string, fsys fs.FS, dir string) error {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return fmt.Errorf("migrations dir %q: %w", dir, err)
	}
	return migrate(context.Background(), dsn, "", sub)
}

// MigrateSchema creates schema if needed and applies fsys inside it, with a
// per-schema goose version table. Each module's SQL uses unqualified table
// names; search_path makes them land in (and later resolve to) its schema,
// with public second so the shared PostGIS types still resolve.
//
// A Postgres session advisory lock (keyed by schema) makes concurrent boots
// safe: a second replica waits for the first to finish instead of racing it
// on the same DDL.
func MigrateSchema(ctx context.Context, dsn, schema string, fsys fs.FS) error {
	if schema == "" {
		return fmt.Errorf("MigrateSchema: empty schema name")
	}
	return migrate(ctx, dsn, schema, fsys)
}

func migrate(ctx context.Context, dsn, schema string, fsys fs.FS) error {
	if schema != "" {
		if err := createSchema(ctx, dsn, schema); err != nil {
			return err
		}
		var err error
		dsn, err = withSearchPath(dsn, schema)
		if err != nil {
			return err
		}
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB for migrations: %w", err)
	}
	defer sqlDB.Close()

	table := "goose_db_version"
	lockName := "public"
	if schema != "" {
		table = pgx.Identifier{schema, "goose_db_version"}.Sanitize()
		lockName = schema
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(lockID("migrate:" + lockName)))
	if err != nil {
		return fmt.Errorf("migration locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithTableName(table),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("migrations for schema %q: %w", schema, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run migrations for schema %q: %w", schema, err)
	}
	return nil
}

func createSchema(ctx context.Context, dsn, schema string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to create schema %q: %w", schema, err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return fmt.Errorf("create schema %q: %w", schema, err)
	}
	return nil
}

// lockID turns a name into the int64 key Postgres advisory locks need.
func lockID(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64() >> 1) // keep it positive, which reads better in pg_locks
}
