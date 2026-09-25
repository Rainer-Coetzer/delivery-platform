package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// as returns an Auth middleware that authenticates every request as c, or
// rejects it like the real middleware when c is nil.
func as(c *auth.Claims) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), c)))
		})
	}
}

type routeCase struct {
	name   string
	claims *auth.Claims
	method string
	path   string
	body   string
	want   int
}

// runRoutes serves each case through routes; stores are nil, so a case
// that slips past its auth check panics instead of passing quietly.
func runRoutes(t *testing.T, cases []routeCase, routes func(claims *auth.Claims) func(chi.Router)) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := chi.NewRouter()
			routes(c.claims)(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}
