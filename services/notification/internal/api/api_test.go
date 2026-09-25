package api

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

func TestMayRead(t *testing.T) {
	cases := []struct {
		name      string
		c         auth.Claims
		recipient string
		want      bool
	}{
		{"own user inbox", auth.Claims{UserID: "u1", Role: auth.RoleCustomer}, "user:u1", true},
		{"another user's inbox", auth.Claims{UserID: "u1", Role: auth.RoleCustomer}, "user:u2", false},
		{"own merchant inbox", auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "r1"}, "merchant:r1", true},
		{"another merchant's inbox", auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "r1"}, "merchant:r2", false},
		{"customer guessing a merchant inbox", auth.Claims{UserID: "u1", Role: auth.RoleCustomer, RefID: "r1"}, "merchant:r1", false},
		{"unlinked merchant vs empty ref", auth.Claims{UserID: "o1", Role: auth.RoleMerchant}, "merchant:", false},
		{"job inbox is admin-only for now", auth.Claims{UserID: "u1", Role: auth.RoleCustomer}, "job:o1", false},
		{"admin reads anything", auth.Claims{Role: auth.RoleAdmin}, "job:o1", true},
	}
	for _, c := range cases {
		if got := MayRead(&c.c, c.recipient); got != c.want {
			t.Errorf("%s: MayRead = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRouteRoles(t *testing.T) {
	customer := &auth.Claims{UserID: "u1", Role: auth.RoleCustomer}
	runRoutes(t, []routeCase{
		{"no token", nil, "GET", "/notifications?recipient=user:u1", "", 401},
		{"another user's inbox", customer, "GET", "/notifications?recipient=user:u2", "", 403},
		{"no recipient", customer, "GET", "/notifications", "", 400},
	}, func(c *auth.Claims) func(chi.Router) { return (&API{Auth: as(c)}).Routes })
}
