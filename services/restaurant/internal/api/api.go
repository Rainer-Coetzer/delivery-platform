// Package api exposes the Restaurant service's REST endpoints: managing
// restaurants and menus, and marking a confirmed order ready for pickup.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/kitchen"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

// Routes: browsing is open to any signed-in user; creating restaurants is
// admin-only; menus and kitchen actions belong to the restaurant itself
// (its account's refId) or an admin.
func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/restaurants", func(r chi.Router) {
		r.Use(a.Auth)
		r.Get("/", a.listRestaurants)
		r.With(auth.RequireRole(auth.RoleAdmin)).Post("/", a.createRestaurant)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", a.getRestaurant)
			r.Get("/menu-items", a.listMenuItems)
			r.Group(func(r chi.Router) {
				r.Use(ownRestaurant)
				r.Post("/menu-items", a.createMenuItem)
				r.Patch("/menu-items/{itemId}", a.updateMenuItem)
				r.Post("/orders/{orderId}/ready", a.markReady)
			})
		})
	})
}

// ownRestaurant allows the restaurant account linked to {id}, or an admin.
func ownRestaurant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.MustFromContext(r.Context()).ActsAs(auth.RoleRestaurant, chi.URLParam(r, "id")) {
			httpx.Forbidden(w, "only this restaurant's account (or an admin) can do this")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type restaurantRequest struct {
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	OpensAt  string  `json:"opensAt"`  // "HH:MM", wall clock in Timezone
	ClosesAt string  `json:"closesAt"` // "HH:MM"; equal to opensAt means open all day
	Timezone string  `json:"timezone"` // IANA, e.g. "Africa/Windhoek"
}

func (a *API) createRestaurant(w http.ResponseWriter, r *http.Request) {
	var req restaurantRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	opens, err1 := time.Parse("15:04", req.OpensAt)
	closes, err2 := time.Parse("15:04", req.ClosesAt)
	if req.Name == "" || err1 != nil || err2 != nil {
		httpx.BadRequest(w, "name, opensAt and closesAt (HH:MM) are required")
		return
	}
	// Required, not defaulted: guessing a zone is how hours ended up being
	// judged in UTC (audit P0-9).
	if _, err := time.LoadLocation(req.Timezone); req.Timezone == "" || err != nil {
		httpx.BadRequest(w, "timezone must be an IANA zone such as Africa/Windhoek")
		return
	}
	rest := &domain.Restaurant{
		ID: uuid.NewString(), Name: req.Name, Lat: req.Lat, Lng: req.Lng,
		OpensAt: sinceMidnight(opens), ClosesAt: sinceMidnight(closes), Timezone: req.Timezone, IsOpen: true,
	}
	if err := a.Store.CreateRestaurant(r.Context(), rest); err != nil {
		a.Log.Error("create restaurant", "error", err)
		httpx.Internal(w, "could not create restaurant")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rest)
}

func sinceMidnight(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func (a *API) listRestaurants(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListRestaurants(r.Context())
	if err != nil {
		httpx.Internal(w, "could not list restaurants")
		return
	}
	if list == nil {
		list = []*domain.Restaurant{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) getRestaurant(w http.ResponseWriter, r *http.Request) {
	rest, err := a.Store.GetRestaurant(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "restaurant not found")
			return
		}
		httpx.Internal(w, "could not load restaurant")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rest)
}

type menuItemRequest struct {
	Name       string `json:"name"`
	PriceCents int64  `json:"priceCents"`
	StockQty   int    `json:"stockQty"`
	Available  bool   `json:"available"`
}

func (a *API) createMenuItem(w http.ResponseWriter, r *http.Request) {
	var req menuItemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" || req.PriceCents <= 0 {
		httpx.BadRequest(w, "name and a positive priceCents are required")
		return
	}
	item := &domain.MenuItem{
		ID: uuid.NewString(), RestaurantID: chi.URLParam(r, "id"),
		Name: req.Name, PriceCents: req.PriceCents, StockQty: req.StockQty, Available: req.Available,
	}
	if err := a.Store.CreateMenuItem(r.Context(), item); err != nil {
		a.Log.Error("create menu item", "error", err)
		httpx.Internal(w, "could not create menu item")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (a *API) listMenuItems(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListMenuItems(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, "could not list menu items")
		return
	}
	if list == nil {
		list = []*domain.MenuItem{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) updateMenuItem(w http.ResponseWriter, r *http.Request) {
	var req menuItemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	item := &domain.MenuItem{
		ID: chi.URLParam(r, "itemId"), RestaurantID: chi.URLParam(r, "id"),
		Name: req.Name, PriceCents: req.PriceCents, StockQty: req.StockQty, Available: req.Available,
	}
	if err := a.Store.UpdateMenuItem(r.Context(), item); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "menu item not found")
			return
		}
		httpx.Internal(w, "could not update menu item")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

// markReady is called by restaurant staff once the kitchen has finished
// preparing a confirmed order. It emits restaurant.order-ready, which the
// Order service uses to move to READY and the Delivery service uses to
// assign a driver.
func (a *API) markReady(w http.ResponseWriter, r *http.Request) {
	err := kitchen.MarkReady(r.Context(), a.Pool, chi.URLParam(r, "id"), chi.URLParam(r, "orderId"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "order not found")
	case errors.Is(err, kitchen.ErrNotPreparing):
		httpx.Conflict(w, "order is not being prepared yet (waiting for payment or already ready)")
	case err != nil:
		a.Log.Error("mark ready", "error", err)
		httpx.Internal(w, "could not mark order ready")
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": store.StatusReady})
	}
}
