// Package api exposes the Assignment service's REST endpoints: agent
// registration and status, and the pickup/completed actions an agent's app
// calls as they progress through an assignment.
package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.Route("/agents", func(r chi.Router) {
		r.Use(a.Auth)
		// Admins onboard agents; the admin then creates the agent's login
		// with refId = this agent's id (identity POST /users).
		r.With(auth.RequireRole(auth.RoleAdmin)).Post("/", a.registerAgent)
		r.With(auth.RequireRole(auth.RoleAdmin)).Get("/", a.listAgents)
		r.Route("/{id}", func(r chi.Router) {
			r.Use(ownAgent)
			r.Patch("/status", a.setAgentStatus)
			r.Patch("/location", a.reportLocation)
		})
	})
	r.Route("/assignments", func(r chi.Router) {
		r.Use(a.Auth)
		r.Get("/{jobId}", a.getAssignment)
		r.Post("/{id}/stops/{seq}/completed", a.stopCompleted)
		// Shortcuts for two-stop jobs (and today's app): the first stop,
		// and the last. On a multi-stop route "completed" is refused
		// while stops remain.
		r.Post("/{id}/picked-up", a.pickedUp)
		r.Post("/{id}/completed", a.completed)
	})
}

// ownAgent allows the agent account linked to {id}, or an admin.
func ownAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.MustFromContext(r.Context()).ActsAs(auth.RoleAgent, chi.URLParam(r, "id")) {
			httpx.Forbidden(w, "only this agent (or an admin) can do this")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// assignedTo reports whether caller is d's agent or an admin.
func assignedTo(caller *auth.Claims, d *store.Assignment) bool {
	return caller.IsAdmin() || (d.AgentID != "" && caller.ActsAs(auth.RoleAgent, d.AgentID))
}

type agentRequest struct {
	Name          string   `json:"name"`
	Vehicle       string   `json:"vehicle"`
	Lat           float64  `json:"lat"`
	Lng           float64  `json:"lng"`
	CapacityGrams int64    `json:"capacityGrams"`
	CapacityCm3   int64    `json:"capacityCm3"`
	Capabilities  []string `json:"capabilities"`
}

func (a *API) registerAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
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
	if req.CapacityGrams < 0 || req.CapacityCm3 < 0 {
		httpx.BadRequest(w, "capacityGrams and capacityCm3 can't be negative (leave them out if not known)")
		return
	}
	d := &store.Agent{ID: uuid.NewString(), Name: req.Name, Vehicle: req.Vehicle, Status: "OFFLINE", Lat: req.Lat, Lng: req.Lng,
		CapacityGrams: req.CapacityGrams, CapacityCm3: req.CapacityCm3, Capabilities: req.Capabilities}
	if err := a.Store.RegisterAgent(r.Context(), d); err != nil {
		a.Log.Error("register agent", "error", err)
		httpx.Internal(w, "could not register agent")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListAgents(r.Context())
	if err != nil {
		httpx.Internal(w, "could not list agents")
		return
	}
	if list == nil {
		list = []*store.Agent{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type statusRequest struct {
	Status string `json:"status"` // AVAILABLE or OFFLINE
}

func (a *API) setAgentStatus(w http.ResponseWriter, r *http.Request) {
	var req statusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Status != "AVAILABLE" && req.Status != "OFFLINE" {
		httpx.BadRequest(w, "status must be AVAILABLE or OFFLINE")
		return
	}
	if err := a.Store.SetAgentStatus(r.Context(), chi.URLParam(r, "id"), req.Status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "agent not found")
			return
		}
		if errors.Is(err, store.ErrAgentBusy) {
			httpx.Conflict(w, "finish the current assignment first")
			return
		}
		httpx.Internal(w, "could not update agent status")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

type locationRequest struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// reportLocation lets a real agent app push GPS coordinates, overriding
// the simulated movement for any assignment currently assigned to this agent.
func (a *API) reportLocation(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	agentID := chi.URLParam(r, "id")
	if err := store.UpdateAgentLocation(r.Context(), a.Pool, agentID, req.Lat, req.Lng); err != nil {
		httpx.Internal(w, "could not update location")
		return
	}
	if _, err := a.Pool.Exec(r.Context(), `UPDATE assignments SET simulated=false WHERE agent_id=$1 AND status IN ('ASSIGNED','PICKED_UP')`, agentID); err != nil {
		a.Log.Error("disable simulation after real gps ping", "error", err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) getAssignment(w http.ResponseWriter, r *http.Request) {
	d, err := a.Store.GetByJob(r.Context(), chi.URLParam(r, "jobId"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "assignment not found")
			return
		}
		httpx.Internal(w, "could not load assignment")
		return
	}
	if !assignedTo(auth.MustFromContext(r.Context()), d) {
		httpx.NotFound(w, "assignment not found")
		return
	}
	if d.Stops, err = store.Stops(r.Context(), a.Pool, d.ID); err != nil {
		a.Log.Error("load assignment stops", "error", err)
		httpx.Internal(w, "could not load assignment")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (a *API) stopCompleted(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(chi.URLParam(r, "seq"))
	if err != nil {
		httpx.BadRequest(w, "stop number must be an integer")
		return
	}
	a.completeStop(w, r, func(*store.Assignment, []store.AssignmentStop) int { return seq })
}

func (a *API) pickedUp(w http.ResponseWriter, r *http.Request) {
	a.completeStop(w, r, func(_ *store.Assignment, stops []store.AssignmentStop) int { return stops[0].Seq })
}

func (a *API) completed(w http.ResponseWriter, r *http.Request) {
	a.completeStop(w, r, func(_ *store.Assignment, stops []store.AssignmentStop) int { return stops[len(stops)-1].Seq })
}

// completeStop completes the stop which picks out of the assignment's
// route, for the assigned agent (or an admin).
func (a *API) completeStop(w http.ResponseWriter, r *http.Request, which func(*store.Assignment, []store.AssignmentStop) int) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	d, err := a.Store.Get(ctx, id)
	if err == nil && !assignedTo(auth.MustFromContext(ctx), d) {
		err = store.ErrNotFound // someone else's assignment: indistinguishable from missing
	}
	var stops []store.AssignmentStop
	if err == nil {
		stops, err = store.Stops(ctx, a.Pool, id)
	}
	if err == nil && len(stops) == 0 {
		err = fmt.Errorf("assignment %s has no route", id)
	}
	seq := 0
	if err == nil {
		seq = which(d, stops)
		err = progress.CompleteStop(ctx, a.Pool, id, seq)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "assignment not found")
	case errors.Is(err, progress.ErrWrongState):
		next := "none: the assignment is finished"
		if d.Next != nil {
			next = strconv.Itoa(d.Next.Seq)
		}
		httpx.Conflict(w, "stop "+strconv.Itoa(seq)+" can't be completed now; next open stop: "+next)
	case err != nil:
		a.Log.Error("complete stop", "error", err)
		httpx.Internal(w, "could not update assignment")
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]int{"completedStop": seq})
	}
}
