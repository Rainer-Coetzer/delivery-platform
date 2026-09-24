//go:build integration

package inprocess

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/bustest"
)

func TestContract(t *testing.T) {
	bustest.Run(t, func(t *testing.T) bustest.Harness {
		pool, dsn := dbtest.NewDSN(t, bustest.Migrations())
		b, err := New(context.Background(), Config{
			DSN:          dsn,
			Retry:        bus.RetryPolicy{Attempts: 3, Backoff: 20 * time.Millisecond},
			PollInterval: 200 * time.Millisecond,
			Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return bustest.Harness{
			Bus:   b,
			Pool:  pool,
			Topic: func(*testing.T) string { return "t-" + uuid.NewString() },
			DeadLetters: func(t *testing.T, topic string) []string {
				all, err := b.DeadLetters(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var keys []string
				for _, d := range all {
					if d.Topic == topic {
						keys = append(keys, d.Key)
					}
				}
				return keys
			},
		}
	})
}

// A key always maps to the same partition, and keys spread across all of
// them (so one stuck job can only block its own partition).
func TestPartitioning(t *testing.T) {
	b := &Bus{cfg: Config{Partitions: 3}}
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		k := uuid.NewString()
		p := b.partitionFor(k)
		if p != b.partitionFor(k) {
			t.Fatalf("key %s moved partitions", k)
		}
		seen[p] = true
	}
	if len(seen) != 3 {
		t.Fatalf("100 keys used partitions %v, want all 3", seen)
	}
}
