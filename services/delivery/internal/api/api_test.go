package api

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

func TestRouteRoles(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	drv1 := &auth.Claims{UserID: "d1", Role: auth.RoleDriver, RefID: "drv1"}
	unlinked := &auth.Claims{UserID: "d2", Role: auth.RoleDriver}
	restaurant := &auth.Claims{UserID: "o1", Role: auth.RoleRestaurant, RefID: "r1"}
	status := `{"status":"AVAILABLE"}`
	runRoutes(t, []routeCase{
		{"no token", nil, "GET", "/drivers", "", 401},
		{"customer registers a driver", customer, "POST", "/drivers", `{}`, 403},
		{"driver registers a driver", drv1, "POST", "/drivers", `{}`, 403},
		{"driver lists drivers", drv1, "GET", "/drivers", "", 403},
		{"driver sets another driver's status", drv1, "PATCH", "/drivers/drv2/status", status, 403},
		{"unlinked driver sets a status", unlinked, "PATCH", "/drivers/drv1/status", status, 403},
		{"customer reports a location", customer, "PATCH", "/drivers/drv1/location", `{"lat":1,"lng":1}`, 403},
		{"restaurant sets driver status", restaurant, "PATCH", "/drivers/drv1/status", status, 403},
	}, func(c *auth.Claims) func(chi.Router) { return (&API{Auth: as(c)}).Routes })
}

func TestAssignedTo(t *testing.T) {
	d := &store.Delivery{DriverID: "drv1"}
	cases := []struct {
		name string
		c    auth.Claims
		want bool
	}{
		{"assigned driver", auth.Claims{Role: auth.RoleDriver, RefID: "drv1"}, true},
		{"other driver", auth.Claims{Role: auth.RoleDriver, RefID: "drv2"}, false},
		{"customer", auth.Claims{Role: auth.RoleCustomer, UserID: "drv1"}, false},
		{"admin", auth.Claims{Role: auth.RoleAdmin}, true},
	}
	for _, c := range cases {
		if got := assignedTo(&c.c, d); got != c.want {
			t.Errorf("%s: assignedTo = %v, want %v", c.name, got, c.want)
		}
	}
}
