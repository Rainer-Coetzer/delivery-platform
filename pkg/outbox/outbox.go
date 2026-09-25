// Package outbox implements the transactional outbox pattern: a service
// writes its domain change and an outbox row in the same Postgres
// transaction, and a background relay polls the table and publishes to
// Kafka, marking rows as sent. This guarantees at-least-once delivery of
// events that always matches what was actually committed — no event is
// published for a transaction that rolled back, and no committed change is
// silently missed.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
)

// Insert writes env into the outbox table inside tx. Call this instead of
// publishing to Kafka directly from request handlers or consumers.
func Insert(ctx context.Context, tx pgx.Tx, env events.Envelope) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO outbox (event_id, event_type, job_id, partition_key, payload, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		env.EventID, env.EventType, nullIfEmpty(env.JobID), partitionKey(env), mustJSON(env), env.Timestamp)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

// partitionKey defaults to jobId; callers that need a different key
// (agent location, user registration) should have already put it in
// JobID when building the envelope, or use InsertWithKey.
func partitionKey(env events.Envelope) string {
	if env.JobID != "" {
		return env.JobID
	}
	return env.EventID
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // envelope always marshals; a failure here is a programmer error
	}
	return b
}

// Publisher is the subset of the Kafka producer the relay needs.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, value []byte) error
}

// Relay polls the outbox table and publishes unsent rows in order, marking
// each as sent once the broker has acknowledged it. Run one relay per
// service instance; if a service is replicated, the SKIP LOCKED clause
// stops instances from double-publishing the same row.
type Relay struct {
	Pool      *db.Pool
	Publisher Publisher
	Log       *slog.Logger
	Service   string // labels the kafka_events_published_total metric
	Interval  time.Duration
	BatchSize int
}

func NewRelay(pool *db.Pool, pub Publisher, log *slog.Logger, service string) *Relay {
	return &Relay{Pool: pool, Publisher: pub, Log: log, Service: service, Interval: 500 * time.Millisecond, BatchSize: 100}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.tick(ctx); err != nil {
				r.Log.Error("outbox relay tick failed", "error", err)
			}
		}
	}
}

type outboxRow struct {
	id           int64
	eventType    string
	partitionKey string
	payload      []byte
}

// tick locks a batch of unsent rows for the duration of the transaction
// (FOR UPDATE SKIP LOCKED only has that effect inside an explicit
// transaction — on a bare, autocommit Query the lock would be released
// before this function could use it), publishes each one, and marks it sent
// before committing. If the process crashes mid-batch, the transaction never
// commits and the rows are picked up again by the next tick or another
// instance — at-least-once delivery, so consumers must be idempotent.
func (r *Relay) tick(ctx context.Context) error {
	return r.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, event_type, partition_key, payload FROM outbox
			 WHERE sent_at IS NULL
			 ORDER BY id
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED`,
			r.BatchSize)
		if err != nil {
			return fmt.Errorf("query outbox: %w", err)
		}
		var batch []outboxRow
		for rows.Next() {
			var row outboxRow
			if err := rows.Scan(&row.id, &row.eventType, &row.partitionKey, &row.payload); err != nil {
				rows.Close()
				return fmt.Errorf("scan outbox row: %w", err)
			}
			batch = append(batch, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, row := range batch {
			if err := r.Publisher.Publish(ctx, row.eventType, row.partitionKey, row.payload); err != nil {
				return fmt.Errorf("publish outbox row %d (topic %s): %w", row.id, row.eventType, err)
			}
			obs.KafkaEventsPublishedTotal.WithLabelValues(r.Service, row.eventType).Inc()
			if _, err := tx.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE id = $1`, row.id); err != nil {
				return fmt.Errorf("mark outbox row %d sent: %w", row.id, err)
			}
		}
		return nil
	})
}
