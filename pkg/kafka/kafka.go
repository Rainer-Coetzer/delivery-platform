// Package kafka wraps franz-go with the conventions this project uses:
// JSON envelopes, a dead-letter topic per consumer after retries are
// exhausted, and one consumer group per service.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
)

// Producer publishes envelopes to Kafka, keyed by partitionKey (usually the
// jobId, so events for the same order land on the same partition and are
// read in order).
type Producer struct {
	client *kgo.Client
}

func NewProducer(brokers []string) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.ProducerLinger(5*time.Millisecond),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordRetries(5),
	)
	if err != nil {
		return nil, fmt.Errorf("new kafka producer: %w", err)
	}
	return &Producer{client: client}, nil
}

// Publish implements outbox.Publisher: value is the JSON-encoded envelope.
func (p *Producer) Publish(ctx context.Context, topic, key string, value []byte) error {
	rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: value}
	res := p.client.ProduceSync(ctx, rec)
	if err := res.FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", topic, err)
	}
	return nil
}

func (p *Producer) Close() { p.client.Close() }

// Handler processes one event; error semantics are bus.Handler's (retry,
// then dead-letter; bus.Permanent skips the retries).
type Handler = bus.Handler

// Consumer subscribes to a fixed set of topics under one consumer group and
// dispatches each record to Handler, retrying transient failures and
// dead-lettering the rest.
type Consumer struct {
	client  *kgo.Client
	log     *slog.Logger
	dlq     *Producer
	service string
	retry   bus.RetryPolicy
}

type ConsumerConfig struct {
	Brokers []string
	GroupID string
	Topics  []string
	Log     *slog.Logger
	DLQ     *Producer       // publishes failed events to "<topic>.dlq"; may be nil to disable
	Retry   bus.RetryPolicy // default bus.DefaultRetry
	// Service labels the kafka_events_consumed_total metric; defaults to
	// GroupID (which is already the service name by this project's
	// convention — see every service's cmd/main.go).
	Service string
}

func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Retry.Attempts == 0 {
		cfg.Retry = bus.DefaultRetry
	}
	if cfg.Service == "" {
		cfg.Service = cfg.GroupID
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("new kafka consumer: %w", err)
	}
	return &Consumer{
		client: client, log: cfg.Log, dlq: cfg.DLQ, service: cfg.Service, retry: cfg.Retry,
	}, nil
}

// Run polls until ctx is cancelled, dispatching every record to handle.
// Offsets are committed once per poll batch, after every record in it is
// handled or dead-lettered; a crash before that means the batch is
// redelivered (at-least-once, consumers dedupe via processed_events).
func (c *Consumer) Run(ctx context.Context, handle Handler) {
	for {
		if ctx.Err() != nil {
			return
		}
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			c.log.Error("fetch error", "topic", topic, "partition", partition, "error", err)
		})
		fetches.EachRecord(func(rec *kgo.Record) {
			if ctx.Err() == nil {
				c.process(ctx, rec, handle)
			}
		})
		// A consumer stopped mid-batch must not commit: records it never
		// finished would be marked done and lost.
		if ctx.Err() != nil {
			return
		}
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			c.log.Error("commit offsets failed", "error", err)
		}
	}
}

func (c *Consumer) process(ctx context.Context, rec *kgo.Record, handle Handler) {
	var env events.Envelope
	if err := json.Unmarshal(rec.Value, &env); err != nil {
		c.log.Error("undecodable record, dead-lettering", "topic", rec.Topic, "error", err)
		c.deadLetter(ctx, rec, "decode error: "+err.Error())
		return
	}
	err, dead := bus.Deliver(ctx, c.retry, bus.Handler(handle), env)
	switch {
	case err == nil:
		obs.KafkaEventsConsumedTotal.WithLabelValues(c.service, rec.Topic, "success").Inc()
	case dead:
		c.log.Error("handler failed, dead-lettering", "topic", rec.Topic, "eventId", env.EventID, "error", err)
		obs.KafkaEventsConsumedTotal.WithLabelValues(c.service, rec.Topic, "dead_lettered").Inc()
		c.deadLetter(ctx, rec, err.Error())
	}
	// Otherwise ctx was cancelled mid-retry: Run returns without committing.
}

// deadLetter publishes rec to "<topic>.dlq", retrying until it succeeds or
// the consumer stops. It used to log and move on, and the commit that
// followed then lost the event (audit P1). Blocking the partition is the
// lesser evil: it's loud in the logs and nothing is lost.
func (c *Consumer) deadLetter(ctx context.Context, rec *kgo.Record, reason string) {
	if c.dlq == nil {
		c.log.Error("no DLQ producer configured; event dropped", "topic", rec.Topic, "reason", reason)
		return
	}
	topic := rec.Topic + ".dlq"
	for attempt := 1; ctx.Err() == nil; attempt++ {
		err := c.dlq.Publish(ctx, topic, string(rec.Key), rec.Value)
		if err == nil {
			return
		}
		c.log.Error("DLQ publish failed, retrying (partition blocked until it succeeds)", "topic", topic, "attempt", attempt, "reason", reason, "error", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

func (c *Consumer) Close() { c.client.Close() }
