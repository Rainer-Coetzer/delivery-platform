// Command gateway is the single entry point for the Flutter clients: JWT
// auth, rate limiting, CORS, reverse proxying to every backend service, and
// a WebSocket hub for live order and driver-location updates.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/kafka"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/proxy"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/ws"
)

const serviceName = "gateway"

func main() {
	log := obs.NewLogger(serviceName)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	issuer := auth.NewIssuer(env("JWT_SECRET", "dev-secret-change-me"), 24*time.Hour)

	customerURL := env("CUSTOMER_SERVICE_URL", "http://customer-service:8081")
	authAPI := api.NewAuthAPI(customerURL, issuer, log)

	hub := ws.NewHub(log)
	brokers := []string{env("KAFKA_BROKERS", "localhost:29092")}
	bridge := &ws.Bridge{Hub: hub, Log: log}

	dlqProducer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Error("kafka dlq producer", "error", err)
		os.Exit(1)
	}
	defer dlqProducer.Close()

	consumer, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers: brokers, GroupID: "gateway-ws",
		Topics: []string{events.OrderStatusChanged, events.DeliveryLocation, events.OrderCancelled},
		Log:    log, DLQ: dlqProducer,
	})
	if err != nil {
		log.Error("kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()
	go consumer.Run(ctx, bridge.Handle)

	router := chi.NewRouter()
	router.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	}))
	router.Use(httpx.RequestLogger(serviceName, log))
	router.Use(httprate.LimitByIP(100, time.Minute))
	router.Get("/healthz", httpx.Health)
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)

	// Auth: registration proxies straight through to the customer service;
	// login needs the gateway's own handler to turn a verified profile into
	// a signed JWT.
	registerProxy, err := proxy.New(customerURL)
	must(err)
	router.Post("/auth/register", registerProxy.ServeHTTP)
	router.Post("/auth/login", authAPI.Login)

	// Live updates: /ws?orderId=<id>. No auth on the socket itself — the
	// orderId is unguessable (a uuid), matching what the REST API already
	// exposes to anyone with the order id.
	router.Get("/ws", hub.ServeHTTP)

	router.Group(func(r chi.Router) {
		r.Use(issuer.Middleware)
		mount(r, "/customers", env("CUSTOMER_SERVICE_URL", "http://customer-service:8081"))
		mount(r, "/restaurants", env("RESTAURANT_SERVICE_URL", "http://restaurant-service:8082"))
		mount(r, "/orders", env("ORDER_SERVICE_URL", "http://order-service:8083"))
		mount(r, "/payments", env("PAYMENT_SERVICE_URL", "http://payment-service:8084"))
		mount(r, "/deliveries", env("DELIVERY_SERVICE_URL", "http://delivery-service:8085"))
		mount(r, "/drivers", env("DELIVERY_SERVICE_URL", "http://delivery-service:8085"))
		mount(r, "/notifications", env("NOTIFICATION_SERVICE_URL", "http://notification-service:8086"))
		mount(r, "/admin", env("ADMIN_SERVICE_URL", "http://admin-service:8087"))
	})

	srv := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("gateway listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

// mount proxies every request under prefix to target unchanged — each
// backend service already serves its routes under that same prefix
// (e.g. the order service serves /orders/*), so the gateway does no path
// rewriting, only auth and routing.
func mount(r chi.Router, prefix, target string) {
	p, err := proxy.New(target)
	must(err)
	r.Mount(prefix, p)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
