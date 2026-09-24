// Command customer-service manages accounts (for every role), addresses,
// and a customer's order-history projection.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/store"
)

const serviceName = "customer-service"

func main() {
	log := obs.NewLogger(serviceName)

	dsn := env("DATABASE_URL", "postgres://customer:customer@localhost:5432/customer_db?sslmode=disable")
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
		Topics: []string{events.OrderCreated, events.OrderStatusChanged, events.OrderCancelled},
		Log:    log, DLQ: dlqProducer,
	})
	if err != nil {
		log.Error("kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	handlers := &consumers.Handlers{Pool: pool, Log: log}
	go consumer.Run(ctx, handlers.Handle)

	secret, err := config.JWTSecret()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}
	st := store.New(pool)
	if err := bootstrapAdmin(ctx, st, log); err != nil {
		log.Error("bootstrap admin", "error", err)
		os.Exit(1)
	}

	a := &api.API{Store: st, Log: log, Auth: auth.NewIssuer(secret, 0).Middleware}
	// No CORS here: browsers only ever talk to the gateway. CORS headers
	// from a backend would be proxied through and duplicate the gateway's,
	// which browsers reject.
	router := chi.NewRouter()
	router.Use(httpx.RequestLogger(serviceName, log))
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)
	a.Routes(router)

	srv := &http.Server{Addr: ":" + env("PORT", "8081"), Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("customer service listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

// bootstrapAdmin creates the first admin from BOOTSTRAP_ADMIN_EMAIL and
// BOOTSTRAP_ADMIN_PASSWORD if that email isn't registered yet. It is the only
// way to get an admin: public registration creates customers only. Both
// unset means "don't bootstrap"; setting just one is a config mistake.
func bootstrapAdmin(ctx context.Context, st *store.Store, log *slog.Logger) error {
	email, password := os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return fmt.Errorf("set both BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD, or neither")
	}
	if !config.IsDev() && len(password) < 12 {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be at least 12 characters outside PROFILE=dev")
	}
	if _, err := st.GetByEmail(ctx, email); err == nil {
		log.Info("bootstrap admin already exists", "email", email)
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	u, err := api.NewUser(config.String("BOOTSTRAP_ADMIN_NAME", "Admin"), email, password, auth.RoleAdmin, "")
	if err != nil {
		return err
	}
	if err := st.CreateUser(ctx, u); err != nil && !errors.Is(err, store.ErrDuplicateEmail) {
		return err // duplicate = another replica won the race, which is fine
	}
	log.Info("bootstrap admin created", "email", email)
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
