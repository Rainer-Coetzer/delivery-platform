// Package api exposes the Restaurant service's REST endpoints: managing
// restaurants and menus, and marking an accepted order ready for pickup.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/restaurants", func(r chi.Router) {
		r.Post("/", a.createRestaurant)
		r.Get("/", a.listRestaurants)
		r.Get("/{id}", a.getRestaurant)
		r.Post("/{id}/menu-items", a.createMenuItem)
		r.Get("/{id}/menu-items", a.listMenuItems)
		r.Patch("/{id}/menu-items/{itemId}", a.updateMenuItem)
		r.Post("/{id}/orders/{orderId}/ready", a.markReady)
	})
}

type restaurantRequest struct {
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	OpensAt  string  `json:"opensAt"`  // "HH:MM"
	ClosesAt string  `json:"closesAt"` // "HH:MM"
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
	rest := &domain.Restaurant{
		ID: uuid.NewString(), Name: req.Name, Lat: req.Lat, Lng: req.Lng,
		OpensAt: sinceMidnight(opens), ClosesAt: sinceMidnight(closes), IsOpen: true,
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
// preparing an accepted order. It emits restaurant.order-ready, which the
// Order service uses to move to READY and the Delivery service uses to
// assign a driver.
func (a *API) markReady(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "orderId")
	ctx := r.Context()

	err := a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		ro, err := store.GetRestaurantOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if ro.Status != "ACCEPTED" {
			return errNotAcceptable
		}
		if err := store.UpdateRestaurantOrderStatus(ctx, tx, orderID, "READY", ""); err != nil {
			return err
		}
		env, err := events.New(events.RestaurantReady, orderID, events.RestaurantReadyData{
			OrderID: orderID, RestaurantID: ro.RestaurantID, Pickup: ro.Pickup, Dropoff: ro.Dropoff,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "order not found")
	case errors.Is(err, errNotAcceptable):
		httpx.Conflict(w, "order is not in an ACCEPTED state")
	case err != nil:
		a.Log.Error("mark ready", "error", err)
		httpx.Internal(w, "could not mark order ready")
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "READY"})
	}
}

var errNotAcceptable = errors.New("order not in acceptable state")
