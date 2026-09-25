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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/blob"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/inprocess"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus/kafkabus"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
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
	// Pack is the vertical pack this install runs (PACK), already validated.
	Pack *packs.Pack
	// Strategies are the pack's pricing, dispatch, ETA, payment and notify
	// choices, already built.
	Strategies *Strategies
	// Blobs stores uploaded files (proof, documents); nil when the pack
	// has neither module.
	Blobs blob.Store
	// Brand is the buyer's brand profile (config/brand.yaml), validated.
	Brand *brand.Brand
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
	// Strategies have no Deps (they're built from params alone), so the ones
	// that log, like the console notify channel, use the default logger.
	// Make that this service's JSON logger rather than Go's plain text one.
	slog.SetDefault(log)
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
	br, err := brand.Load(BrandFile(), config.IsDev())
	if err != nil {
		return err
	}
	pack, err := LoadPack()
	if err != nil {
		return err
	}
	strategies, err := BuildStrategies(pack)
	if err != nil {
		return err
	}
	if err := br.Apply(pack, strategies.Payments.Methods()); err != nil {
		return err
	}
	// The brand may offer fewer payment methods than the pack takes.
	if strategies.Payments, err = strategies.Payments.Only(br.EnabledPaymentMethods(pack, strategies.Payments.Methods())); err != nil {
		return err
	}
	if pack.Name != br.Pack {
		log.Warn("PACK overrides the brand's pack; the brand's pack overrides don't apply", "pack", pack.Name, "brandPack", br.Pack)
	}
	log.Info("brand and pack loaded", "brand", br.Name, "pack", pack.Name, "modules", pack.Modules, "payment", strategies.Payments.Methods(),
		"currency", br.Currency.Code, "tenant", br.TenantID)
	blobs, err := openBlobs(pack)
	if err != nil {
		return err
	}
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
	router.Use(maxBody(1<<20, isUpload))
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
		deps := Deps{Log: log.With("module", def.Name), Bus: b, Issuer: issuer, Auth: issuer.Middleware, HTTP: httpClient, URL: urlFor, Embedded: opts.Embedded, Pack: pack, Strategies: strategies, Blobs: blobs, Brand: br}
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

// openBlobs opens the file store when the pack keeps files. BLOB_DIR is
// required outside dev: files written to a container's own filesystem
// vanish with the container, and nobody notices until they're needed.
func openBlobs(p *packs.Pack) (blob.Store, error) {
	if !p.Has(packs.ModProof) && !p.Has(packs.ModDocuments) {
		return nil, nil
	}
	dir := os.Getenv("BLOB_DIR")
	if dir == "" {
		if !config.IsDev() {
			return nil, errors.New("BLOB_DIR is required: pack " + p.Name + " stores files (proof or documents); point it at a persistent volume")
		}
		dir = filepath.Join("data", "blobs")
	}
	return blob.NewLocal(dir)
}

// LoadPack loads the pack this platform runs: PACK when set (tests, the
// e2e, a demo of another pack), otherwise the brand file's pack. From
// PACKS_DIR when set, otherwise from the packs embedded in the binary. In
// the `full` profile every service loads it, from the same brand file.
func LoadPack() (*packs.Pack, error) {
	name, err := packName()
	if err != nil {
		return nil, err
	}
	var fsys fs.FS = packfs.FS
	if dir := os.Getenv("PACKS_DIR"); dir != "" {
		fsys = os.DirFS(dir)
	}
	roles := []string{string(auth.RoleCustomer), string(auth.RoleMerchant), string(auth.RoleAgent), string(auth.RoleAdmin)}
	p, err := packs.Load(fsys, name, lifecycle.Known{Events: events.AllTopics, Roles: roles})
	if err != nil {
		return nil, fmt.Errorf("PACK=%s: %w", name, err)
	}
	return p, nil
}

// BrandFile is where the brand profile lives: BRAND_FILE, or
// config/brand.yaml from the working directory (the repo root, for go run
// and the tools; the image sets BRAND_FILE).
func BrandFile() string { return config.String("BRAND_FILE", filepath.Join("config", "brand.yaml")) }

// packName is PACK, or the brand file's pack. It reads that one field only:
// the tools need the pack's name, not a fully validated brand.
func packName() (string, error) {
	if name := os.Getenv("PACK"); name != "" {
		return name, nil
	}
	name, err := brand.PackName(BrandFile())
	if err != nil {
		return "", fmt.Errorf("which pack? set PACK or fix the brand file: %w", err)
	}
	return name, nil
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
// stops a client from streaming gigabytes into a decoder. Uploads are the
// exception, and each upload handler caps its body itself, at the size the
// pack's settings allow (blob.Store.Put's max).
func maxBody(n int64, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !exempt(r) {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isUpload is the upload routes: proof on an assignment stop and
// documents on a job. Keep in step with those modules' routes.
func isUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	return (strings.HasPrefix(p, "/assignments/") && strings.HasSuffix(p, "/proof")) ||
		(strings.HasPrefix(p, "/jobs/") && strings.HasSuffix(p, "/documents"))
}
