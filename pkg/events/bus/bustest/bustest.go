// Package bustest is the contract every bus.Bus implementation must pass
// (ADR-0002). Each implementation's integration test calls Run with a
// factory; the assertions live here once, so "interchangeable" is tested
// rather than hoped for.
package bustest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations create processed_events and a counter table for the dedupe
// test. Factories apply them to the database they hand back in Harness.Pool.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // embed pattern and directory disagree: a build-time mistake
	}
	return sub
}

// Harness is what one implementation provides to the suite.
type Harness struct {
	Bus bus.Bus
	// Pool is a database with Migrations applied (for handler side effects).
	Pool *db.Pool
	// Topic returns a fresh topic name for this test (Kafka needs them
	// created up front, dead-letter topic included).
	Topic func(t *testing.T) string
	// DeadLetters returns keys dead-lettered on topic so far.
	DeadLetters func(t *testing.T, topic string) []string
}

// Factory builds a fresh harness per test. Retry backoff should be short.
type Factory func(t *testing.T) Harness

// Timeout for anything eventually-consistent. Kafka group joins take
// seconds; the in-process bus is usually done in milliseconds.
const Timeout = 45 * time.Second

func Run(t *testing.T, factory Factory) {
	t.Run("OrderedPerKey", func(t *testing.T) { orderedPerKey(t, factory(t)) })
	t.Run("EveryGroupSeesEveryEvent", func(t *testing.T) { everyGroup(t, factory(t)) })
	t.Run("TransientErrorIsRetried", func(t *testing.T) { transientRetried(t, factory(t)) })
	t.Run("PermanentErrorIsDeadLetteredFirstTime", func(t *testing.T) { permanentDeadLettered(t, factory(t)) })
	t.Run("AtLeastOnceAfterStopMidEvent", func(t *testing.T) { atLeastOnce(t, factory(t)) })
	t.Run("DuplicatesDedupedByProcessedEvents", func(t *testing.T) { deduped(t, factory(t)) })
}

// publish sends an envelope whose JobID is key and whose EventID is id.
func publish(t *testing.T, b bus.Bus, topic, key, id string, n int) {
	t.Helper()
	env := events.Envelope{EventID: id, EventType: topic, JobID: key, Timestamp: time.Now().UTC(), Data: json.RawMessage(strconv.Itoa(n))}
	raw, _ := json.Marshal(env)
	if err := b.Publish(context.Background(), topic, key, raw); err != nil {
		t.Fatalf("publish %s/%s: %v", key, id, err)
	}
}

func subscribe(t *testing.T, b bus.Bus, group, topic string, h bus.Handler) func() {
	t.Helper()
	stop, err := b.Subscribe(context.Background(), group, []string{topic}, h)
	if err != nil {
		t.Fatalf("subscribe %s: %v", group, err)
	}
	t.Cleanup(stop) // stop twice is fine: cancel is idempotent, Wait returns
	return stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(Timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", Timeout, what)
}

func orderedPerKey(t *testing.T, h Harness) {
	topic := h.Topic(t)
	var mu sync.Mutex
	seen := map[string][]int{}
	subscribe(t, h.Bus, "g-"+uuid.NewString(), topic, func(_ context.Context, env events.Envelope) error {
		n, _ := strconv.Atoi(string(env.Data))
		mu.Lock()
		seen[env.JobID] = append(seen[env.JobID], n)
		mu.Unlock()
		return nil
	})
	keys := []string{"job-a", "job-b", "job-c"}
	const perKey = 20
	for i := 0; i < perKey; i++ {
		for _, k := range keys {
			publish(t, h.Bus, topic, k, uuid.NewString(), i)
		}
	}
	eventually(t, "all events", func() bool {
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for _, v := range seen {
			total += len(v)
		}
		return total >= perKey*len(keys)
	})
	mu.Lock()
	defer mu.Unlock()
	for _, k := range keys {
		for i, n := range seen[k] {
			if n != i {
				t.Fatalf("key %s out of order: got sequence %v", k, seen[k])
			}
		}
	}
}

func everyGroup(t *testing.T, h Harness) {
	topic := h.Topic(t)
	var a, b sync.Map
	count := func(m *sync.Map) int {
		n := 0
		m.Range(func(_, _ any) bool { n++; return true })
		return n
	}
	subscribe(t, h.Bus, "ga-"+uuid.NewString(), topic, func(_ context.Context, env events.Envelope) error { a.Store(env.EventID, true); return nil })
	subscribe(t, h.Bus, "gb-"+uuid.NewString(), topic, func(_ context.Context, env events.Envelope) error { b.Store(env.EventID, true); return nil })
	for i := 0; i < 10; i++ {
		publish(t, h.Bus, topic, fmt.Sprintf("job-%d", i), uuid.NewString(), i)
	}
	eventually(t, "both groups see all 10", func() bool { return count(&a) == 10 && count(&b) == 10 })
}

func transientRetried(t *testing.T, h Harness) {
	topic := h.Topic(t)
	var mu sync.Mutex
	attempts, done := 0, false
	subscribe(t, h.Bus, "g-"+uuid.NewString(), topic, func(_ context.Context, env events.Envelope) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts < 3 {
			return errors.New("temporarily unavailable")
		}
		done = true
		return nil
	})
	publish(t, h.Bus, topic, "job-retry", uuid.NewString(), 1)
	eventually(t, "success on the third attempt", func() bool { mu.Lock(); defer mu.Unlock(); return done })
	if dl := h.DeadLetters(t, topic); len(dl) != 0 {
		t.Fatalf("a recovered event must not be dead-lettered, got %v", dl)
	}
}

func permanentDeadLettered(t *testing.T, h Harness) {
	topic := h.Topic(t)
	var mu sync.Mutex
	calls := 0
	subscribe(t, h.Bus, "g-"+uuid.NewString(), topic, func(_ context.Context, env events.Envelope) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return bus.Permanent(errors.New("invalid transition"))
	})
	publish(t, h.Bus, topic, "job-poison", uuid.NewString(), 1)
	eventually(t, "dead letter", func() bool { return len(h.DeadLetters(t, topic)) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("a permanent error must not be retried: handler called %d times", calls)
	}
	if got := h.DeadLetters(t, topic); got[0] != "job-poison" {
		t.Fatalf("dead letter key = %q, want job-poison", got[0])
	}
}

// atLeastOnce: stop the consumer while it's inside event 3 (the handler
// returns only when its context is cancelled). The new consumer in the same
// group must deliver event 3 again, and nothing may be lost.
func atLeastOnce(t *testing.T, h Harness) {
	topic := h.Topic(t)
	group := "g-" + uuid.NewString()
	for i := 1; i <= 5; i++ {
		publish(t, h.Bus, topic, "job-crash", fmt.Sprintf("evt-%d", i), i)
	}

	var mu sync.Mutex
	counts := map[string]int{}
	inThird := make(chan struct{})
	var once sync.Once
	stop := subscribe(t, h.Bus, group, topic, func(ctx context.Context, env events.Envelope) error {
		mu.Lock()
		counts[env.EventID]++
		mu.Unlock()
		if env.EventID == "evt-3" {
			once.Do(func() { close(inThird) })
			<-ctx.Done() // "crash" while handling
			return ctx.Err()
		}
		return nil
	})
	select {
	case <-inThird:
	case <-time.After(Timeout):
		t.Fatal("never reached event 3")
	}
	stop()

	subscribe(t, h.Bus, group, topic, func(_ context.Context, env events.Envelope) error {
		mu.Lock()
		counts[env.EventID]++
		mu.Unlock()
		return nil
	})
	eventually(t, "every event delivered after restart", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for i := 1; i <= 5; i++ {
			if counts[fmt.Sprintf("evt-%d", i)] == 0 {
				return false
			}
		}
		return counts["evt-3"] >= 2 && counts["evt-5"] >= 1
	})
}

// deduped: the same event id published twice (what a redelivery looks like)
// must have one effect when the handler uses db.InsertIfNew, which is the
// pattern every consumer in this repo follows.
func deduped(t *testing.T, h Harness) {
	topic := h.Topic(t)
	var mu sync.Mutex
	handled := 0
	subscribe(t, h.Bus, "g-"+uuid.NewString(), topic, func(ctx context.Context, env events.Envelope) error {
		mu.Lock()
		handled++
		mu.Unlock()
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO effects (event_id, applied) VALUES ($1, 1)`, env.EventID)
			return err
		})
	})
	id := uuid.NewString()
	publish(t, h.Bus, topic, "job-dup", id, 1)
	publish(t, h.Bus, topic, "job-dup", id, 1)
	eventually(t, "both assignments handled", func() bool { mu.Lock(); defer mu.Unlock(); return handled >= 2 })
	var n int
	if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM effects WHERE event_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("effects applied %d times, want exactly 1", n)
	}
}
