// Command delivery-service assigns drivers to ready orders, tracks
// deliveries through pickup and completion, and simulates driver movement
// for the live map.
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
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/kafka"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/sim"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

const serviceName = "delivery-service"

func main() {
	log := obs.NewLogger(serviceName)

	// Validate config before touching the database: a misconfigured service
	// must refuse to start without having run migrations first.
	secret, err := config.JWTSecret()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}

	dsn := env("DATABASE_URL", "postgres://delivery:delivery@localhost:5432/delivery_db?sslmode=disable")
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

	dlqProducer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka dlq producer", "error", err)
		os.Exit(1)
	}
	defer dlqProducer.Close()

	consumer, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: serviceName,
		Topics: consumers.Topics,
		Log:    log, DLQ: dlqProducer,
	})
	if err != nil {
		log.Error("kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	st := store.New(pool)
	handlers := &consumers.Handlers{Pool: pool, Log: log}
	go consumer.Run(ctx, handlers.Handle)

	retrier := &sim.PendingAssignmentRetrier{Pool: pool, Store: st, Log: log}
	go retrier.Run(ctx)

	locationProducer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka location producer", "error", err)
		os.Exit(1)
	}
	defer locationProducer.Close()
	autopilot, err := config.Bool("DEMO_AUTOPILOT", false)
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}
	// 60m per 2s tick is a believable city speed but makes a demo order
	// take minutes; the dev compose override raises it.
	stepMeters, err := config.Float("SIM_STEP_METERS", 60)
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}
	log.Info("driver simulation", "autopilot", autopilot, "stepMeters", stepMeters)
	simulator := &sim.LocationSimulator{Pool: pool, Store: st, Producer: locationProducer, Log: log, StepMeters: stepMeters, Autopilot: autopilot}
	go simulator.Run(ctx)

	a := &api.API{Pool: pool, Store: st, Log: log, Auth: auth.NewIssuer(secret, 0).Middleware}
	router := chi.NewRouter()
	// No CORS: only the gateway is browser-facing (see customer-service).
	router.Use(httpx.RequestLogger(serviceName, log))
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)
	a.Routes(router)

	srv := &http.Server{Addr: ":" + env("PORT", "8085"), Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("delivery service listening", "addr", srv.Addr)
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
