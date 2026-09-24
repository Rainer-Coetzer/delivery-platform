// Command admin-service projects order, restaurant-decision and delivery
// events into a read model and serves reports over it.
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
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/store"
)

const serviceName = "admin-service"

func main() {
	log := obs.NewLogger(serviceName)

	// Validate config before touching the database: a misconfigured service
	// must refuse to start without having run migrations first.
	secret, err := config.JWTSecret()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}

	dsn := env("DATABASE_URL", "postgres://admin:admin@localhost:5432/admin_db?sslmode=disable")
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
	dlqProducer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka dlq producer", "error", err)
		os.Exit(1)
	}
	defer dlqProducer.Close()

	consumer, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: serviceName,
		Topics: []string{events.OrderCreated, events.OrderStatusChanged, events.OrderCancelled, events.DeliveryAssigned},
		Log:    log, DLQ: dlqProducer,
	})
	if err != nil {
		log.Error("kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	handlers := &consumers.Handlers{Pool: pool, Log: log}
	go consumer.Run(ctx, handlers.Handle)

	a := &api.API{Store: store.New(pool), Auth: auth.NewIssuer(secret, 0).Middleware}
	router := chi.NewRouter()
	// No CORS: only the gateway is browser-facing (see customer-service).
	router.Use(httpx.RequestLogger(serviceName, log))
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)
	a.Routes(router)

	srv := &http.Server{Addr: ":" + env("PORT", "8087"), Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("admin service listening", "addr", srv.Addr)
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
