// Package db wraps a pgx pool with a transaction helper and the
// idempotency check every consumer uses before applying an event.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	*pgxpool.Pool
}

// Queryer is satisfied by both *Pool and pgx.Tx, so store functions that
// only need to run a statement can accept either a bare pool connection or
// an in-flight transaction without two copies of the same query.
type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func Open(ctx context.Context, dsn string) (*Pool, error) {
	return OpenSchema(ctx, dsn, "", 0)
}

// OpenSchema opens a pool whose connections use search_path=<schema>,public,
// so a module's unqualified SQL reads and writes its own schema (ADR-0002).
// maxConns > 0 caps the pool: in the platform binary every module has its
// own pool against one Postgres, and small caps keep the total well under
// max_connections and stop one busy module from starving the rest.
func OpenSchema(ctx context.Context, dsn, schema string, maxConns int32) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if schema != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = searchPath(schema)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool (schema %q): %w", schema, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping (schema %q): %w", schema, err)
	}
	return &Pool{pool}, nil
}

func searchPath(schema string) string {
	return pgx.Identifier{schema}.Sanitize() + ", public"
}

// withSearchPath adds search_path to a DSN for database/sql users (goose).
func withSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	q := u.Query()
	q.Set("search_path", searchPath(schema))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// WithTx runs fn inside a transaction, committing on success and rolling
// back on error or panic.
func (p *Pool) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			panic(r)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()
	err = fn(tx)
	return err
}

// InsertIfNew inserts eventID into processed_events inside tx and reports
// whether this is the first time the event has been seen (isNew == true) or
// it was already processed — a duplicate delivery the caller should skip
// applying side effects for, while still committing the transaction.
func InsertIfNew(ctx context.Context, tx pgx.Tx, eventID, eventType string) (isNew bool, err error) {
	ct, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING`,
		eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("insert processed_events: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

var ErrNotFound = errors.New("not found")
