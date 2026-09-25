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
	agent := &auth.Claims{UserID: "d1", Role: auth.RoleAgent, RefID: "drv1"}

	cases := []struct {
		name   string
		claims *auth.Claims
		method string
		path   string
		body   string
		want   int
	}{
		{"register as admin", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","role":"admin"}`, 400},
		{"register as merchant", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","role":"merchant"}`, 400},
		{"register claiming a refId", nil, "POST", "/auth/register", `{"name":"x","email":"x@y.z","password":"password123","refId":"r1"}`, 400},
		{"create user without token", nil, "POST", "/users", `{}`, 401},
		{"customer creating users", customer, "POST", "/users", `{"name":"x","email":"x@y.z","password":"password123","role":"admin"}`, 403},
		{"agent creating users", agent, "POST", "/users", `{}`, 403},
		{"no token on profile", nil, "GET", "/customers/u1", "", 401},
		{"customer reading another profile", customer, "GET", "/customers/u2", "", 403},
		{"customer reading another's jobs", customer, "GET", "/customers/u2/jobs", "", 403},
		{"customer adding another's address", customer, "POST", "/customers/u2/addresses", `{"label":"x"}`, 403},
		{"admin: merchant account needs refId", &auth.Claims{UserID: "a", Role: auth.RoleAdmin}, "POST", "/users", `{"name":"x","email":"x@y.z","password":"password123","role":"merchant"}`, 400},
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

// A platform only has the account kinds its brand enables: a courier brand
// with no merchants can't be given merchant logins, and a B2B brand that
// lists no customers has no public sign-up.
func TestBrandRoles(t *testing.T) {
	admin := &auth.Claims{UserID: "x", Role: auth.RoleAdmin}
	for _, c := range []struct {
		name, method, path, body string
		claims                   *auth.Claims
		roles                    []string
		want                     int
	}{
		{"merchant login on a brand without merchants", "POST", "/users", `{"name":"a","email":"a@b.c","password":"password123","role":"merchant","refId":"m1"}`, admin, []string{"customer", "agent", "admin"}, 400},
		{"sign-up on a brand without customers", "POST", "/auth/register", `{"name":"a","email":"a@b.c","password":"password123"}`, nil, []string{"merchant", "agent", "admin"}, 403},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := &API{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c.claims), Roles: c.roles}
			r := chi.NewRouter()
			a.Routes(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if rec.Code != c.want {
				t.Fatalf("%d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}
