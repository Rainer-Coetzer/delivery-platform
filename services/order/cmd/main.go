// Command order-service runs the Order microservice: REST API, outbox
// relay, and Kafka consumer, per docs/ARCHITECTURE.md.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/kafka"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/catalogue"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/store"
)

const serviceName = "order-service"

func main() {
	log := obs.NewLogger(serviceName)

	// Validate config before touching the database: a misconfigured service
	// must refuse to start without having run migrations first.
	secret, err := config.JWTSecret()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}

	dsn := env("DATABASE_URL", "postgres://order:order@localhost:5432/order_db?sslmode=disable")
	if err := db.Migrate(dsn, migrations.FS, "."); err != nil {
		log.Error("migrate", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		log.Error("open db", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	brokers := []string{env("KAFKA_BROKERS", "localhost:29092")}
	producer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	relay := outbox.NewRelay(pool, producer, log, serviceName)
	go relay.Run(ctx)

	consumerTopics := []string{
		events.RestaurantAccepted, events.RestaurantRejected, events.RestaurantReady,
		events.PaymentCompleted, events.PaymentFailed,
		events.DeliveryAssigned, events.DeliveryPickedUp, events.DeliveryCompleted,
	}
	dlqProducer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka dlq producer", "error", err)
		os.Exit(1)
	}
	defer dlqProducer.Close()

	consumer, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: serviceName, Topics: consumerTopics, Log: log, DLQ: dlqProducer,
	})
	if err != nil {
		log.Error("kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	handlers := &consumers.Handlers{Pool: pool, Log: log}
	go consumer.Run(ctx, handlers.Handle)

	a := &api.API{
		Pool: pool, Store: store.New(pool), Log: log,
		Catalogue: catalogue.New(env("RESTAURANT_SERVICE_URL", "http://restaurant-service:8082")),
		Auth:      auth.NewIssuer(secret, 0).Middleware,
	}
	// No CORS: only the gateway is browser-facing (see customer-service).
	router := chi.NewRouter()
	router.Use(httpx.RequestLogger(serviceName, log))
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)
	a.Routes(router)

	srv := &http.Server{Addr: ":" + env("PORT", "8083"), Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("order service listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
