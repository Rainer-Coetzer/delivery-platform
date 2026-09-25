package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
)

func TestCanView(t *testing.T) {
	o := &domain.Job{CustomerID: "cust1", MerchantID: "rest1", AgentID: "drv1"}
	unassigned := &domain.Job{CustomerID: "cust1", MerchantID: "rest1"}
	cases := []struct {
		name string
		c    auth.Claims
		job  *domain.Job
		want bool
	}{
		{"own customer", auth.Claims{UserID: "cust1", Role: auth.RoleCustomer}, o, true},
		{"other customer", auth.Claims{UserID: "cust2", Role: auth.RoleCustomer}, o, false},
		{"own merchant", auth.Claims{Role: auth.RoleMerchant, RefID: "rest1"}, o, true},
		{"other merchant", auth.Claims{Role: auth.RoleMerchant, RefID: "rest2"}, o, false},
		{"assigned agent", auth.Claims{Role: auth.RoleAgent, RefID: "drv1"}, o, true},
		{"other agent", auth.Claims{Role: auth.RoleAgent, RefID: "drv2"}, o, false},
		{"unlinked agent vs unassigned job", auth.Claims{Role: auth.RoleAgent}, unassigned, false},
		{"customer whose id equals a merchant id", auth.Claims{UserID: "rest1", Role: auth.RoleCustomer}, o, false},
		{"admin", auth.Claims{Role: auth.RoleAdmin}, o, true},
	}
	for _, c := range cases {
		if got := CanView(&c.c, c.job); got != c.want {
			t.Errorf("%s: CanView = %v, want %v", c.name, got, c.want)
		}
	}
}

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

// Rejections decided before any store or catalogue call (both nil here).
func TestRouteRoles(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	merchant := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "r1"}
	agent := &auth.Claims{UserID: "d1", Role: auth.RoleAgent, RefID: "drv1"}

	cases := []struct {
		name   string
		claims *auth.Claims
		method string
		path   string
		body   string
		want   int
	}{
		{"no token", nil, "GET", "/jobs/o1", "", 401},
		{"merchant places a job", merchant, "POST", "/jobs", `{}`, 403},
		{"agent places a job", agent, "POST", "/jobs", `{}`, 403},
		{"customer lists another customer's jobs", customer, "GET", "/jobs?customerId=u2", "", 403},
		{"merchant lists another merchant's jobs", merchant, "GET", "/jobs?merchantId=r2", "", 403},
		{"merchant lists a customer's jobs", merchant, "GET", "/jobs?customerId=u1", "", 403},
		{"agent lists a merchant's jobs", agent, "GET", "/jobs?merchantId=r1", "", 403},
		{"customer job without merchant", customer, "POST", "/jobs", `{"paymentMethod":"cash"}`, 400},
		{"transition without a target", customer, "POST", "/jobs/o1/transitions", `{}`, 400},
		{"approval on a pack without the module", customer, "POST", "/jobs/o1/facts/approved", ``, 404},
		{"payment method the pack doesn't take", customer, "POST", "/jobs", `{"merchantId":"r1","paymentMethod":"bitcoin","dropoff":{"lat":-22.5,"lng":17.1}}`, 400}, // else it reaches the (nil) catalogue,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &API{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c.claims), Pack: foodPack(t), Payments: foodStrategies(t).Payments}
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

func TestValidDropoff(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     bool
	}{
		{"windhoek", -22.56, 17.08, true},
		{"omitted decodes to 0,0", 0, 0, false},
		{"lat out of range", 91, 17, false},
		{"lng out of range", -22, 181, false},
		{"equator is fine when lng isn't 0", 0, 17.08, true},
	}
	for _, c := range cases {
		if got := validDropoff(events.Location{Lat: c.lat, Lng: c.lng}); got != c.want {
			t.Errorf("%s: validDropoff = %v, want %v", c.name, got, c.want)
		}
	}
}

func foodPack(t *testing.T) *packs.Pack {
	t.Helper()
	p, err := packs.Load(packfs.FS, "food", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func foodStrategies(t *testing.T) *app.Strategies {
	t.Helper()
	s, err := app.BuildStrategies(foodPack(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
