package api

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

func TestRouteRoles(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	drv1 := &auth.Claims{UserID: "d1", Role: auth.RoleAgent, RefID: "drv1"}
	unlinked := &auth.Claims{UserID: "d2", Role: auth.RoleAgent}
	merchant := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "r1"}
	status := `{"status":"AVAILABLE"}`
	runRoutes(t, []routeCase{
		{"no token", nil, "GET", "/agents", "", 401},
		{"customer registers an agent", customer, "POST", "/agents", `{}`, 403},
		{"agent registers an agent", drv1, "POST", "/agents", `{}`, 403},
		{"agent lists agents", drv1, "GET", "/agents", "", 403},
		{"agent sets another agent's status", drv1, "PATCH", "/agents/drv2/status", status, 403},
		{"unlinked agent sets a status", unlinked, "PATCH", "/agents/drv1/status", status, 403},
		{"customer reports a location", customer, "PATCH", "/agents/drv1/location", `{"lat":1,"lng":1}`, 403},
		{"merchant sets agent status", merchant, "PATCH", "/agents/drv1/status", status, 403},
	}, func(c *auth.Claims) func(chi.Router) { return (&API{Auth: as(c)}).Routes })
}

func TestAssignedTo(t *testing.T) {
	d := &store.Assignment{AgentID: "drv1"}
	cases := []struct {
		name string
		c    auth.Claims
		want bool
	}{
		{"assigned agent", auth.Claims{Role: auth.RoleAgent, RefID: "drv1"}, true},
		{"other agent", auth.Claims{Role: auth.RoleAgent, RefID: "drv2"}, false},
		{"customer", auth.Claims{Role: auth.RoleCustomer, UserID: "drv1"}, false},
		{"admin", auth.Claims{Role: auth.RoleAdmin}, true},
	}
	for _, c := range cases {
		if got := assignedTo(&c.c, d); got != c.want {
			t.Errorf("%s: assignedTo = %v, want %v", c.name, got, c.want)
		}
	}
}
