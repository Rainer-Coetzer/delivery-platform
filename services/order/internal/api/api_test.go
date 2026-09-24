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
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
)

func TestCanView(t *testing.T) {
	o := &domain.Order{CustomerID: "cust1", RestaurantID: "rest1", DriverID: "drv1"}
	unassigned := &domain.Order{CustomerID: "cust1", RestaurantID: "rest1"}
	cases := []struct {
		name  string
		c     auth.Claims
		order *domain.Order
		want  bool
	}{
		{"own customer", auth.Claims{UserID: "cust1", Role: auth.RoleCustomer}, o, true},
		{"other customer", auth.Claims{UserID: "cust2", Role: auth.RoleCustomer}, o, false},
		{"own restaurant", auth.Claims{Role: auth.RoleRestaurant, RefID: "rest1"}, o, true},
		{"other restaurant", auth.Claims{Role: auth.RoleRestaurant, RefID: "rest2"}, o, false},
		{"assigned driver", auth.Claims{Role: auth.RoleDriver, RefID: "drv1"}, o, true},
		{"other driver", auth.Claims{Role: auth.RoleDriver, RefID: "drv2"}, o, false},
		{"unlinked driver vs unassigned order", auth.Claims{Role: auth.RoleDriver}, unassigned, false},
		{"customer whose id equals a restaurant id", auth.Claims{UserID: "rest1", Role: auth.RoleCustomer}, o, false},
		{"admin", auth.Claims{Role: auth.RoleAdmin}, o, true},
	}
	for _, c := range cases {
		if got := CanView(&c.c, c.order); got != c.want {
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
	restaurant := &auth.Claims{UserID: "o1", Role: auth.RoleRestaurant, RefID: "r1"}
	driver := &auth.Claims{UserID: "d1", Role: auth.RoleDriver, RefID: "drv1"}

	cases := []struct {
		name   string
		claims *auth.Claims
		method string
		path   string
		body   string
		want   int
	}{
		{"no token", nil, "GET", "/orders/o1", "", 401},
		{"restaurant places an order", restaurant, "POST", "/orders", `{}`, 403},
		{"driver places an order", driver, "POST", "/orders", `{}`, 403},
		{"customer lists another customer's orders", customer, "GET", "/orders?customerId=u2", "", 403},
		{"restaurant lists another restaurant's orders", restaurant, "GET", "/orders?restaurantId=r2", "", 403},
		{"restaurant lists a customer's orders", restaurant, "GET", "/orders?customerId=u1", "", 403},
		{"driver lists a restaurant's orders", driver, "GET", "/orders?restaurantId=r1", "", 403},
		{"customer order without restaurant", customer, "POST", "/orders", `{"paymentMethod":"cash"}`, 400},
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
