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
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
)

// Producer publishes envelopes to Kafka, keyed by partitionKey (usually the
// orderId, so events for the same order land on the same partition and are
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

// Handler processes one event. Returning an error causes a retry (up to
// MaxRetries) and then the event is sent to the dead-letter topic.
type Handler func(ctx context.Context, env events.Envelope) error

// Consumer subscribes to a fixed set of topics under one consumer group and
// dispatches each record to Handler, retrying transient failures and
// dead-lettering the rest.
type Consumer struct {
	client     *kgo.Client
	log        *slog.Logger
	dlq        *Producer
	service    string
	maxRetries int
	retryDelay time.Duration
}

type ConsumerConfig struct {
	Brokers    []string
	GroupID    string
	Topics     []string
	Log        *slog.Logger
	DLQ        *Producer // publishes failed events to "<topic>.dlq"; may be nil to disable
	MaxRetries int       // default 3
	// Service labels the kafka_events_consumed_total metric; defaults to
	// GroupID (which is already the service name by this project's
	// convention — see every service's cmd/main.go).
	Service string
}

func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
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
		client: client, log: cfg.Log, dlq: cfg.DLQ, service: cfg.Service,
		maxRetries: cfg.MaxRetries, retryDelay: 200 * time.Millisecond,
	}, nil
}

// Run polls until ctx is cancelled, dispatching every record to handle.
// Commits offsets after each record is handled (successfully or
// dead-lettered) so a crash never silently skips a message.
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
			c.process(ctx, rec, handle)
		})
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

	var lastErr error
	for attempt := 1; attempt <= c.maxRetries; attempt++ {
		lastErr = handle(ctx, env)
		if lastErr == nil {
			obs.KafkaEventsConsumedTotal.WithLabelValues(c.service, rec.Topic, "success").Inc()
			return
		}
		c.log.Warn("handler failed, retrying",
			"topic", rec.Topic, "eventId", env.EventID, "attempt", attempt, "error", lastErr)
		select {
		case <-time.After(c.retryDelay * time.Duration(attempt)):
		case <-ctx.Done():
			return
		}
	}
	c.log.Error("handler exhausted retries, dead-lettering",
		"topic", rec.Topic, "eventId", env.EventID, "error", lastErr)
	obs.KafkaEventsConsumedTotal.WithLabelValues(c.service, rec.Topic, "dead_lettered").Inc()
	c.deadLetter(ctx, rec, lastErr.Error())
}

func (c *Consumer) deadLetter(ctx context.Context, rec *kgo.Record, reason string) {
	if c.dlq == nil {
		return
	}
	topic := rec.Topic + ".dlq"
	if err := c.dlq.Publish(ctx, topic, string(rec.Key), rec.Value); err != nil {
		c.log.Error("failed to publish to DLQ", "topic", topic, "reason", reason, "error", err)
	}
}

func (c *Consumer) Close() { c.client.Close() }
