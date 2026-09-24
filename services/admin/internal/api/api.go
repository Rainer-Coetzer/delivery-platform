// Package api exposes read-only reports over the admin read model.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/store"
)

type API struct {
	Store *store.Store
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(a.Auth, auth.RequireRole(auth.RoleAdmin))
		r.Get("/overview", a.overview)
		r.Get("/restaurants/{id}/stats", a.restaurantStats)
		r.Get("/drivers/{id}/stats", a.driverStats)
	})
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	o, err := a.Store.Overview(r.Context())
	if err != nil {
		httpx.Internal(w, "could not load overview")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (a *API) restaurantStats(w http.ResponseWriter, r *http.Request) {
	s, err := a.Store.RestaurantStats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, "could not load restaurant stats")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (a *API) driverStats(w http.ResponseWriter, r *http.Request) {
	s, err := a.Store.DriverStats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, "could not load driver stats")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}
