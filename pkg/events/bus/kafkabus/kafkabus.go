// Package kafkabus adapts pkg/kafka to bus.Bus, for the `full` deployment
// (EVENTS_BUS=kafka). Topics must already exist; infra/kafka/topics.sh
// creates them, including every "<topic>.dlq".
package kafkabus

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/kafka"
)

type Config struct {
	Brokers []string
	Retry   bus.RetryPolicy
	Log     *slog.Logger
}

type Bus struct {
	cfg      Config
	producer *kafka.Producer // publishes events and dead letters alike
}

func New(cfg Config) (*Bus, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	p, err := kafka.NewProducer(cfg.Brokers)
	if err != nil {
		return nil, err
	}
	return &Bus{cfg: cfg, producer: p}, nil
}

func (b *Bus) Publish(ctx context.Context, topic, key string, value []byte) error {
	return b.producer.Publish(ctx, topic, key, value)
}

func (b *Bus) Subscribe(ctx context.Context, group string, topics []string, h bus.Handler) (func(), error) {
	if group == "" || len(topics) == 0 || h == nil {
		return nil, fmt.Errorf("subscribe: group, topics and handler are required")
	}
	c, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: b.cfg.Brokers, GroupID: group, Topics: topics,
		Log: b.cfg.Log, DLQ: b.producer, Retry: b.cfg.Retry,
	})
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Run(sctx, h)
	}()
	return func() {
		cancel()
		wg.Wait()
		c.Close()
	}, nil
}

func (b *Bus) Close() { b.producer.Close() }
