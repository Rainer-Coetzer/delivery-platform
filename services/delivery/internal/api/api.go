// Package api exposes the Delivery service's REST endpoints: driver
// registration and status, and the pickup/completed actions a driver's app
// calls as they progress through a delivery.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/drivers", func(r chi.Router) {
		r.Post("/", a.registerDriver)
		r.Get("/", a.listDrivers)
		r.Patch("/{id}/status", a.setDriverStatus)
		r.Patch("/{id}/location", a.reportLocation)
	})
	r.Route("/deliveries", func(r chi.Router) {
		r.Get("/{orderId}", a.getDelivery)
		r.Post("/{id}/picked-up", a.pickedUp)
		r.Post("/{id}/completed", a.completed)
	})
}

type driverRequest struct {
	Name    string  `json:"name"`
	Vehicle string  `json:"vehicle"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

func (a *API) registerDriver(w http.ResponseWriter, r *http.Request) {
	var req driverRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" {
		httpx.BadRequest(w, "name is required")
		return
	}
	if req.Vehicle == "" {
		req.Vehicle = "bike"
	}
	d := &store.Driver{ID: uuid.NewString(), Name: req.Name, Vehicle: req.Vehicle, Status: "OFFLINE", Lat: req.Lat, Lng: req.Lng}
	if err := a.Store.RegisterDriver(r.Context(), d); err != nil {
		a.Log.Error("register driver", "error", err)
		httpx.Internal(w, "could not register driver")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (a *API) listDrivers(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListDrivers(r.Context())
	if err != nil {
		httpx.Internal(w, "could not list drivers")
		return
	}
	if list == nil {
		list = []*store.Driver{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type statusRequest struct {
	Status string `json:"status"` // AVAILABLE or OFFLINE
}

func (a *API) setDriverStatus(w http.ResponseWriter, r *http.Request) {
	var req statusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Status != "AVAILABLE" && req.Status != "OFFLINE" {
		httpx.BadRequest(w, "status must be AVAILABLE or OFFLINE")
		return
	}
	if err := a.Store.SetDriverStatus(r.Context(), chi.URLParam(r, "id"), req.Status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "driver not found")
			return
		}
		httpx.Internal(w, "could not update driver status")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

type locationRequest struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// reportLocation lets a real driver app push GPS coordinates, overriding
// the simulated movement for any delivery currently assigned to this driver.
func (a *API) reportLocation(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	driverID := chi.URLParam(r, "id")
	if err := store.UpdateDriverLocation(r.Context(), a.Pool, driverID, req.Lat, req.Lng); err != nil {
		httpx.Internal(w, "could not update location")
		return
	}
	if _, err := a.Pool.Exec(r.Context(), `UPDATE deliveries SET simulated=false WHERE driver_id=$1 AND status IN ('ASSIGNED','PICKED_UP')`, driverID); err != nil {
		a.Log.Error("disable simulation after real gps ping", "error", err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) getDelivery(w http.ResponseWriter, r *http.Request) {
	d, err := a.Store.GetByOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "delivery not found")
			return
		}
		httpx.Internal(w, "could not load delivery")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (a *API) pickedUp(w http.ResponseWriter, r *http.Request) {
	a.transition(w, r, lifecycle.StatusAssigned, lifecycle.StatusPickedUp)
}

func (a *API) completed(w http.ResponseWriter, r *http.Request) {
	a.transition(w, r, lifecycle.StatusPickedUp, lifecycle.StatusCompleted)
}

func (a *API) transition(w http.ResponseWriter, r *http.Request, from, to string) {
	err := lifecycle.Advance(r.Context(), a.Pool, chi.URLParam(r, "id"), from, to)
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "delivery not found")
	case errors.Is(err, lifecycle.ErrWrongState):
		httpx.Conflict(w, "delivery is not in the "+from+" state")
	case err != nil:
		a.Log.Error("delivery transition", "error", err)
		httpx.Internal(w, "could not update delivery")
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": to})
	}
}
