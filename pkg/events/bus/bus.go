// Package bus is the event transport every module publishes to and consumes
// from. Two implementations pass the same contract suite (bustest):
//
//   - inprocess (default, ADR-0002): a partitioned log in Postgres with
//     per-group offsets and a dead-letter table. No extra infrastructure.
//   - kafkabus: Kafka via franz-go, for the `full` deployment.
//
// Both guarantee the same things, and nothing more:
//   - events with the same key are delivered to a group in publish order
//   - at-least-once: a crash before a group commits means redelivery, so
//     handlers must be idempotent (db.InsertIfNew in the same transaction)
//   - every group gets every event on its topics
//   - an error wrapped with Permanent is dead-lettered on the first attempt;
//     other errors are retried, then dead-lettered.
package bus

import (
	"context"
	"errors"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

// Handler processes one event. See the package doc for error semantics.
type Handler func(ctx context.Context, env events.Envelope) error

// Bus publishes and delivers events.
type Bus interface {
	// Publish appends value (a JSON-encoded events.Envelope) to topic,
	// ordered with every other event that has the same key.
	Publish(ctx context.Context, topic, key string, value []byte) error
	// Subscribe starts delivering topics to h under group, in the
	// background. The returned stop function cancels delivery and blocks
	// until in-flight handlers have returned.
	Subscribe(ctx context.Context, group string, topics []string, h Handler) (stop func(), err error)
	// Close releases the bus's own resources. Stop subscriptions first.
	Close()
}

// RetryPolicy controls how a failing handler is retried before its event
// is dead-lettered. Both implementations use it identically.
type RetryPolicy struct {
	Attempts int           // total attempts, including the first
	Backoff  time.Duration // wait before attempt n+1 is Backoff*n
}

// DefaultRetry: 3 attempts over ~0.6s. Long enough to ride out a lock or a
// restarting dependency, short enough not to stall a partition.
var DefaultRetry = RetryPolicy{Attempts: 3, Backoff: 200 * time.Millisecond}

type permanentError struct{ err error }

func (p permanentError) Error() string { return "permanent: " + p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks err as not worth retrying (an invalid state transition, a
// payload that will never decode), so the event goes straight to the dead
// letters instead of stalling its partition for the whole retry budget.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Deliver runs h against env with policy, returning nil on success, or the
// last error plus whether the event should be dead-lettered. It returns
// dead=false with ctx's error if ctx is cancelled mid-retry: a stopping
// consumer must not dead-letter (or commit) an event it never finished.
func Deliver(ctx context.Context, policy RetryPolicy, h Handler, env events.Envelope) (err error, dead bool) {
	attempts := policy.Attempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		err = h(ctx, env)
		if err == nil {
			return nil, false
		}
		if ctx.Err() != nil {
			return ctx.Err(), false
		}
		if IsPermanent(err) {
			return err, true
		}
		if attempt == attempts {
			break
		}
		select {
		case <-time.After(policy.Backoff * time.Duration(attempt)):
		case <-ctx.Done():
			return ctx.Err(), false
		}
	}
	return err, true
}
