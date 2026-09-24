package api

import (
	"io"
	"log/slog"
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

// Every case here must be decided before the handler touches the store (the
// store is nil), which is also the property we want: reject first.
func TestSecurityBoundaries(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	driver := &auth.Claims{UserID: "d1", Role: auth.RoleDriver, RefID: "drv1"}

	cases := []struct {
		name   string
		claims *auth.Claims
		method string
		path   string
		body   string
		want   int
	}{
		{"register as admin", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","role":"admin"}`, 400},
		{"register as restaurant", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","role":"restaurant"}`, 400},
		{"register claiming a refId", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","refId":"r1"}`, 400},
		{"create user without token", nil, "POST", "/users", `{}`, 401},
		{"customer creating users", customer, "POST", "/users", `{"name":"x","email":"x@y.z","password":"password123","role":"admin"}`, 403},
		{"driver creating users", driver, "POST", "/users", `{}`, 403},
		{"no token on profile", nil, "GET", "/customers/u1", "", 401},
		{"customer reading another profile", customer, "GET", "/customers/u2", "", 403},
		{"customer reading another's orders", customer, "GET", "/customers/u2/orders", "", 403},
		{"customer adding another's address", customer, "POST", "/customers/u2/addresses", `{"label":"x"}`, 403},
		{"admin: restaurant account needs refId", &auth.Claims{UserID: "a", Role: auth.RoleAdmin}, "POST", "/users", `{"name":"x","email":"x@y.z","password":"password123","role":"restaurant"}`, 400},
		{"admin: unknown role", &auth.Claims{UserID: "a", Role: auth.RoleAdmin}, "POST", "/users", `{"name":"x","email":"x@y.z","password":"password123","role":"root"}`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &API{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c.claims)}
			r := chi.NewRouter()
			a.Routes(r)
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}
