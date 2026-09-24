// Package obs wires up structured logging and Prometheus metrics the same
// way in every service.
package obs

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewLogger returns a JSON slog.Logger tagged with the service name, so logs
// from every container can be told apart once they're all in one place.
func NewLogger(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h).With("service", service)
}

// MetricsHandler serves Prometheus metrics at /metrics.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// Standard metrics every service registers so Grafana can build one
// dashboard that works across all of them.
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled, by route and status.",
	}, []string{"service", "route", "status"})

	KafkaEventsConsumedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kafka_events_consumed_total",
		Help: "Total Kafka events consumed, by topic and outcome.",
	}, []string{"service", "topic", "outcome"})

	KafkaEventsPublishedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "kafka_events_published_total",
		Help: "Total Kafka events published via the outbox relay, by topic.",
	}, []string{"service", "topic"})

	OutboxLagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "outbox_lag_seconds",
		Help: "Age of the oldest unsent outbox row, in seconds.",
	}, []string{"service"})
)
