// Package gateway is the edge module: login (turning a verified account into
// a JWT), the live-tracking WebSocket, CORS and rate limits. Between
// microservices it also reverse-proxies each path prefix to its service;
// inside cmd/platform every module already shares the router, so it doesn't.
package gateway

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/proxy"
	"github.com/rainercoetzer/delivery-platform/services/gateway/internal/ws"
)

// Definition: no Migrations, the gateway keeps no state of its own.
var Definition = app.Definition{Name: "gateway", New: newModule}

func newModule(d app.Deps) (*app.Module, error) {
	origins, err := config.AllowedOrigins()
	if err != nil {
		return nil, err
	}
	d.Log.Info("security config", "profile", config.Profile(), "allowedOrigins", origins)

	authAPI := api.NewAuthAPI(d.URL("identity"), d.HTTP, d.Issuer, d.Log)
	hub := ws.NewHub(d.Log)
	hub.OriginPatterns = originHosts(origins)
	hub.Authorize = ws.NewJobAccess(d.URL("jobs"), d.HTTP, d.Issuer).Authorize
	bridge := &ws.Bridge{Hub: hub, Log: d.Log}

	routes := func(r chi.Router) {
		r.Post("/auth/login", authAPI.Login)
		// Live updates: /ws?jobId=<id>&token=<jwt>. The hub verifies the
		// token and asks the jobs module whether this user may see it.
		r.Get("/ws", hub.ServeHTTP)
		// What the app needs to draw this platform: the pack's words,
		// lifecycle and limits. Public (read before login); nothing secret.
		r.Get("/config", configHandler(d.Brand, d.Pack, d.Strategies.Payments.Methods()))
		r.Get("/brand/logo", logoHandler(d.Brand))
		if d.Embedded {
			return // the other modules are mounted on this same router
		}
		// Between microservices: registration goes straight through, and
		// every other prefix is proxied after an early token check. Each
		// service verifies the token again (docs/adr/0001).
		register, err := proxy.New(d.URL("identity"))
		must(err)
		r.Post("/auth/register", register.ServeHTTP)
		r.Group(func(r chi.Router) {
			r.Use(d.Auth)
			for prefix, module := range proxiedPrefixes {
				p, err := proxy.New(d.URL(module))
				must(err)
				r.Mount(prefix, p)
			}
		})
	}

	edge := func(h http.Handler) http.Handler {
		h = blockInternal(h)
		h = httprate.LimitByIP(100, time.Minute)(h)
		return cors.Handler(cors.Options{
			AllowedOrigins: origins, AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type"},
		})(h)
	}

	return &app.Module{
		Routes: routes,
		Edge:   edge,
		Consumers: []app.Consumer{{
			Group:   "gateway-ws",
			Topics:  []string{events.JobStatusChanged, events.DispatchLocation, events.JobCancelled},
			Handler: bridge.Handle,
		}},
	}, nil
}

// internalOnly are routes other modules call in-process but the internet
// must not reach. In the platform binary every module shares the router
// Caddy exposes, so without this /auth/verify (the credential check behind
// login) would be public. In-process calls never pass through the edge, so
// they're unaffected. Between microservices these paths simply aren't
// proxied.
var internalOnly = map[string]bool{"/auth/verify": true}

func blockInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if internalOnly[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// proxiedPrefixes: which module owns each public path prefix.
var proxiedPrefixes = map[string]string{
	"/customers": "identity", "/users": "identity",
	"/merchants": "merchant", "/jobs": "jobs", "/payments": "payment",
	"/assignments": "dispatch", "/agents": "dispatch", "/offers": "dispatch",
	"/notifications": "notification", "/admin": "reporting",
}

// originHosts converts ALLOWED_ORIGINS entries (full origins like
// "https://app.example.com") into the host patterns coder/websocket
// compares against. Passing the full URL would silently match nothing.
func originHosts(origins []string) []string {
	hosts := make([]string, 0, len(origins))
	for _, o := range origins {
		if o == "*" {
			hosts = append(hosts, "*")
			continue
		}
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}

// must: proxy.New only fails on a malformed URL, which is a config error
// worth crashing on at startup.
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// publicConfig is GET /config: the brand (who the platform is, how it
// looks, where it works) beside the pack's configuration (what it does
// and in which words).
type publicConfig struct {
	Brand brandInfo `json:"brand"`
	packs.PublicConfig
}

type brandInfo struct {
	*brand.Brand
	LogoURL string `json:"logoUrl,omitempty"`
}

// configHandler serves the public configuration in the caller's locale:
// ?locale=af, else the first Accept-Language tag, else the brand's
// default. Roles and payment methods are what the brand enables, which
// may be fewer than the pack has.
func configHandler(b *brand.Brand, p *packs.Pack, paymentMethods []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			first, _, _ := strings.Cut(r.Header.Get("Accept-Language"), ",")
			locale, _, _ = strings.Cut(strings.TrimSpace(first), "-")
			locale, _, _ = strings.Cut(locale, ";")
		}
		locale = strings.ToLower(locale)
		if !slices.Contains(b.Locales.Supported, locale) {
			locale = b.Locales.Default
		}
		cfg := publicConfig{Brand: brandInfo{Brand: b}, PublicConfig: p.Config(locale, paymentMethods)}
		cfg.Roles = b.EnabledRoles(p)
		if path, _ := b.LogoFile(); path != "" {
			cfg.Brand.LogoURL = "/brand/logo"
		}
		// It changes only when the brand or pack does (a redeploy): let
		// clients and proxies cache it briefly.
		w.Header().Set("Cache-Control", "public, max-age=300")
		httpx.WriteJSON(w, http.StatusOK, cfg)
	}
}

// logoHandler serves the brand's logo. An SVG can carry script, so it goes
// out with a policy that forbids any: opened on its own, it's a picture.
func logoHandler(b *brand.Brand) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, contentType := b.LogoFile()
		if path == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, r, path)
	}
}
