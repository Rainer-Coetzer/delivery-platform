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

// Route-role matrix for the rejection paths. Store and pool are nil, so any
// case that slipped past the checks would panic instead of passing.
func TestRouteRoles(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	agent := &auth.Claims{UserID: "d1", Role: auth.RoleAgent, RefID: "drv1"}
	r1 := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "r1"}
	unlinked := &auth.Claims{UserID: "o2", Role: auth.RoleMerchant}
	admin := &auth.Claims{UserID: "a1", Role: auth.RoleAdmin}

	catalogue := `{"name":"x","priceCents":100,"stockQty":1,"available":true}`
	cases := []struct {
		name   string
		claims *auth.Claims
		method string
		path   string
		body   string
		want   int
	}{
		{"list without token", nil, "GET", "/merchants", "", 401},
		{"customer creates merchant", customer, "POST", "/merchants", `{}`, 403},
		{"merchant creates merchant", r1, "POST", "/merchants", `{}`, 403},
		{"customer edits a catalogue", customer, "POST", "/merchants/r1/catalogue-items", catalogue, 403},
		{"agent edits a catalogue", agent, "PATCH", "/merchants/r1/catalogue-items/i1", catalogue, 403},
		{"r1 edits r2's catalogue", r1, "POST", "/merchants/r2/catalogue-items", catalogue, 403},
		{"unlinked merchant account edits a catalogue", unlinked, "POST", "/merchants/r1/catalogue-items", catalogue, 403},
		{"r1 marks r2's job ready", r1, "POST", "/merchants/r2/jobs/o1/ready", "", 403},
		{"customer marks ready", customer, "POST", "/merchants/r1/jobs/o1/ready", "", 403},
		{"admin: create without timezone", admin, "POST", "/merchants", `{"name":"x","opensAt":"09:00","closesAt":"22:00"}`, 400},
		{"admin: create with bad timezone", admin, "POST", "/merchants", `{"name":"x","opensAt":"09:00","closesAt":"22:00","timezone":"Mars/Olympus"}`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &API{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c.claims)}
			r := chi.NewRouter()
			a.Routes(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}
