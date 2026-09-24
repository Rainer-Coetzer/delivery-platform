//go:build integration

package kafkabus

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/bustest"
)

// Needs a broker: TEST_KAFKA_BROKERS=localhost:29092 (the compose `full`
// profile's host listener). Skips without it, like dbtest does.
func TestContract(t *testing.T) {
	brokers := os.Getenv("TEST_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("TEST_KAFKA_BROKERS not set; skipping Kafka contract test")
	}
	seeds := strings.Split(brokers, ",")
	admin, err := kgo.NewClient(kgo.SeedBrokers(seeds...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	adm := kadm.NewClient(admin)

	bustest.Run(t, func(t *testing.T) bustest.Harness {
		pool := dbtest.New(t, bustest.Migrations())
		b, err := New(Config{
			Brokers: seeds,
			Retry:   bus.RetryPolicy{Attempts: 3, Backoff: 20 * time.Millisecond},
			Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)

		var dlMu sync.Mutex
		return bustest.Harness{
			Bus:  b,
			Pool: pool,
			Topic: func(t *testing.T) string {
				topic := "bustest-" + uuid.NewString()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				// 3 partitions like production topics, plus its .dlq.
				if _, err := adm.CreateTopic(ctx, 3, 1, nil, topic); err != nil {
					t.Fatalf("create %s: %v", topic, err)
				}
				if _, err := adm.CreateTopic(ctx, 1, 1, nil, topic+".dlq"); err != nil {
					t.Fatalf("create %s.dlq: %v", topic, err)
				}
				t.Cleanup(func() { _, _ = adm.DeleteTopics(context.Background(), topic, topic+".dlq") })
				// CreateTopic returns before every partition has a leader; a
				// produce in that window fails with UNKNOWN_TOPIC_OR_PARTITION.
				for ctx.Err() == nil {
					md, err := adm.Metadata(ctx, topic, topic+".dlq")
					if err == nil && ready(md.Topics[topic], 3) && ready(md.Topics[topic+".dlq"], 1) {
						return topic
					}
					time.Sleep(100 * time.Millisecond)
				}
				t.Fatalf("topic %s never became ready", topic)
				return topic
			},
			DeadLetters: func(t *testing.T, topic string) []string {
				dlMu.Lock()
				defer dlMu.Unlock()
				return readAll(t, seeds, topic+".dlq")
			},
		}
	})
}

func ready(td kadm.TopicDetail, partitions int) bool {
	if td.Err != nil || len(td.Partitions) != partitions {
		return false
	}
	for _, p := range td.Partitions {
		if p.Leader < 0 || p.Err != nil {
			return false
		}
	}
	return true
}

// readAll returns the keys of every record currently on topic.
func readAll(t *testing.T, seeds []string, topic string) []string {
	t.Helper()
	c, err := kgo.NewClient(kgo.SeedBrokers(seeds...), kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var keys []string
	for ctx.Err() == nil {
		f := c.PollFetches(ctx)
		f.EachRecord(func(r *kgo.Record) { keys = append(keys, string(r.Key)) })
	}
	return keys
}
