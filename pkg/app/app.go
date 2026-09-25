// Package app is the service kit (ADR-0002). A module (identity, jobs,
// dispatch, ...) describes itself once, as a Definition. The same
// Definition then runs either:
//
//   - alone, as a microservice (services/<name>/cmd): its own database,
//     Kafka, real HTTP between services; or
//   - together with the others in cmd/platform: one database with a schema
//     per module, the in-process bus, and in-memory HTTP between modules.
//
// Run owns everything a module shouldn't have to repeat: fail-fast config,
// migrations, pools, the bus, outbox relays, consumers, the router, health
// endpoints, timeouts and graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/inprocess"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/kafkabus"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
)

// Definition is how a module describes itself to Run.
type Definition struct {
	// Name is the module's short name ("jobs"). In the platform it is also
	// the database schema.
	Name string
	// FormerSchemas are names this module's schema had in earlier releases
	// (Phase C renamed "orders" to "jobs", and so on). On boot, a former
	// schema is renamed to Name so an upgraded install keeps its data.
	// Keep entries for as long as an old install might still upgrade.
	FormerSchemas []string
	// Migrations are the module's goose .sql files at the FS root. Nil means
	// the module has no database (the gateway).
	Migrations fs.FS
	// New builds the module once its database is migrated and open.
	New func(Deps) (*Module, error)
}

// Module is what a Definition builds.
type Module struct {
	Routes    func(chi.Router) // mounted on the shared router; no /healthz or /metrics (Run adds them)
	Consumers []Consumer
	Workers   []func(context.Context)
	// Outbox runs a relay from this module's outbox table into the bus.
	Outbox bool
	// Edge wraps the external handler only (CORS, rate limits). Calls
	// between modules skip it. Only the gateway sets it.
	Edge func(http.Handler) http.Handler
}

type Consumer struct {
	Group   string // Kafka consumer group / bus group; stable across deployments
	Topics  []string
	Handler bus.Handler
}

// Deps are the shared things a module is built from.
type Deps struct {
	Log    *slog.Logger
	Pool   *db.Pool // this module's own pool (schema-scoped in the platform)
	Bus    bus.Bus
	Issuer *auth.Issuer
	Auth   func(http.Handler) http.Handler // Issuer.Middleware
	// HTTP and URL are for calling other modules: in-memory inside the
	// platform, the network between microservices. Always forward the
	// caller's Authorization header so the callee applies its own authz.
	HTTP *http.Client
	URL  func(module string) string
	// Embedded is true inside cmd/platform, where every module shares one
	// router (the gateway doesn't proxy there).
	Embedded bool
}

// Options for Run.
type Options struct {
	Name        string // process name for logs and metrics
	Definitions []Definition
	Embedded    bool
	DefaultPort string
}

// defaultServiceURLs are where each module lives in the `full` (microservice)
// compose profile, where every service listens on 8080 in its own
// container. Override with <NAME>_SERVICE_URL.
var defaultServiceURLs = map[string]string{
	"identity": "http://identity-service:8080", "merchant": "http://merchant-service:8080",
	"jobs": "http://jobs-service:8080", "payment": "http://payment-service:8080",
	"dispatch": "http://dispatch-service:8080", "notification": "http://notification-service:8080",
	"reporting": "http://reporting-service:8080",
}

// Run starts the modules and blocks until SIGINT/SIGTERM. Any startup error
// is logged with its cause and exits non-zero: a half-started service is
// worse than a stopped one.
func Run(opts Options) {
	log := obs.NewLogger(opts.Name)
	if err := run(log, opts); err != nil {
		log.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, opts Options) error {
	// Config first, before anything touches the database or the broker.
	secret, err := config.JWTSecret()
	if err != nil {
		return err
	}
	issuer := auth.NewIssuer(secret, 24*time.Hour)
	needsDB := false
	for _, d := range opts.Definitions {
		needsDB = needsDB || d.Migrations != nil
	}
	dsn := os.Getenv("DATABASE_URL")
	busKind := config.String("EVENTS_BUS", map[bool]string{true: "inprocess", false: "kafka"}[opts.Embedded])
	if (needsDB || busKind == "inprocess") && dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	if busKind == "inprocess" && !opts.Embedded {
		return errors.New("EVENTS_BUS=inprocess only works in cmd/platform (one shared database); use kafka for separate services")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := newBus(ctx, busKind, dsn, log)
	if err != nil {
		return err
	}
	defer b.Close()

	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Use(httpx.RequestLogger(opts.Name, log))
	router.Use(maxBody(1 << 20))
	router.Get("/metrics", obs.MetricsHandler().ServeHTTP)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	urlFor := func(module string) string {
		return config.String(strings.ToUpper(module)+"_SERVICE_URL", defaultServiceURLs[module])
	}
	if opts.Embedded {
		httpClient = NewInprocClient(router)
		urlFor = func(string) string { return InternalBaseURL }
	}

	var pools []*db.Pool
	defer func() {
		for _, p := range pools {
			p.Close()
		}
	}()
	var stops []func()
	defer func() {
		for _, s := range stops {
			s()
		}
	}()
	edge := func(h http.Handler) http.Handler { return h }
	// Deferred last, so it runs first: stop relays and workers before the
	// pools and the bus they use are closed (the error path doesn't get
	// SIGTERM's cancellation for free).
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	for _, def := range opts.Definitions {
		deps := Deps{Log: log.With("module", def.Name), Bus: b, Issuer: issuer, Auth: issuer.Middleware, HTTP: httpClient, URL: urlFor, Embedded: opts.Embedded}
		if def.Migrations != nil {
			pool, err := openModuleDB(ctx, dsn, def, opts.Embedded)
			if err != nil {
				return err
			}
			pools = append(pools, pool)
			deps.Pool = pool
		}
		mod, err := def.New(deps)
		if err != nil {
			return fmt.Errorf("module %s: %w", def.Name, err)
		}
		if mod.Routes != nil {
			mod.Routes(router)
		}
		if mod.Edge != nil {
			edge = mod.Edge
		}
		if mod.Outbox {
			go outbox.NewRelay(deps.Pool, b, deps.Log, def.Name).Run(ctx)
		}
		for _, c := range mod.Consumers {
			s, err := b.Subscribe(ctx, c.Group, c.Topics, c.Handler)
			if err != nil {
				return fmt.Errorf("module %s: subscribe %s: %w", def.Name, c.Group, err)
			}
			stops = append(stops, s)
		}
		for _, w := range mod.Workers {
			go w(ctx)
		}
		log.Info("module started", "module", def.Name, "consumers", len(mod.Consumers), "workers", len(mod.Workers))
	}

	// Liveness never touches dependencies (a DB blip mustn't get the process
	// killed); readiness does (so a proxy stops routing to us meanwhile).
	router.Get("/healthz", httpx.Health)
	router.Get("/readyz", readiness(pools))

	srv := &http.Server{
		Addr:    ":" + config.String("PORT", opts.DefaultPort),
		Handler: edge(router),
		// Slowloris guard: a client must send its headers promptly.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr, "bus", busKind, "modules", len(opts.Definitions), "profile", config.Profile())
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return nil
}

func newBus(ctx context.Context, kind, dsn string, log *slog.Logger) (bus.Bus, error) {
	switch kind {
	case "inprocess":
		return inprocess.New(ctx, inprocess.Config{DSN: dsn, Log: log})
	case "kafka":
		brokers := strings.Split(config.String("KAFKA_BROKERS", "localhost:29092"), ",")
		return kafkabus.New(kafkabus.Config{Brokers: brokers, Log: log})
	default:
		return nil, fmt.Errorf("EVENTS_BUS=%q: use inprocess or kafka", kind)
	}
}

// openModuleDB migrates and opens a module's database: its own schema in
// the shared database inside the platform, the whole database otherwise.
func openModuleDB(ctx context.Context, dsn string, def Definition, embedded bool) (*db.Pool, error) {
	if embedded {
		if err := db.MigrateSchema(ctx, dsn, def.Name, def.Migrations, def.FormerSchemas...); err != nil {
			return nil, fmt.Errorf("module %s: %w", def.Name, err)
		}
		// 5 connections per module: 9 modules + the bus stay well under
		// Postgres' default 100, with headroom for backups and psql.
		return db.OpenSchema(ctx, dsn, def.Name, 5)
	}
	if err := db.Migrate(dsn, def.Migrations, "."); err != nil {
		return nil, fmt.Errorf("module %s: %w", def.Name, err)
	}
	return db.Open(ctx, dsn)
}

func readiness(pools []*db.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, p := range pools {
			if err := p.Ping(ctx); err != nil {
				httpx.WriteError(w, http.StatusServiceUnavailable, "not_ready", "database unreachable")
				return
			}
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// maxBody caps request bodies. Every JSON body in this API is small; this
// stops a client from streaming gigabytes into a decoder. Uploads (the
// documents and proof modules, later) will need their own limit.
func maxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
