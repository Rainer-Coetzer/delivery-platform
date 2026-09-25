package api

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

func TestAdminOnly(t *testing.T) {
	var cases []routeCase
	for _, role := range []auth.Role{auth.RoleCustomer, auth.RoleMerchant, auth.RoleAgent} {
		for _, p := range []string{"/admin/overview", "/admin/merchants/r1/stats", "/admin/agents/d1/stats"} {
			cases = append(cases, routeCase{string(role) + " " + p, &auth.Claims{UserID: "x", Role: role, RefID: "r1"}, "GET", p, "", 403})
		}
	}
	cases = append(cases, routeCase{"no token", nil, "GET", "/admin/overview", "", 401})
	runRoutes(t, cases, func(c *auth.Claims) func(chi.Router) { return (&API{Auth: as(c)}).Routes })
}
