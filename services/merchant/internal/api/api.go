// Package api exposes the Merchant service's REST endpoints: managing
// merchants and catalogues, and marking a confirmed job ready for pickup.
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
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/prep"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/store"
)

type API struct {
	Pool  *db.Pool
	Store *store.Store
	Log   *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
	// DefaultTimezone is the brand's, for merchants created without one.
	DefaultTimezone string
}

// Routes: browsing is open to any signed-in user; creating merchants is
// admin-only; catalogues and prep actions belong to the merchant itself
// (its account's refId) or an admin.
func (a *API) Routes(r chi.Router) {
	r.Route("/merchants", func(r chi.Router) {
		r.Use(a.Auth)
		r.Get("/", a.listMerchants)
		r.With(auth.RequireRole(auth.RoleAdmin)).Post("/", a.createMerchant)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", a.getMerchant)
			r.Get("/catalogue-items", a.listCatalogueItems)
			r.Group(func(r chi.Router) {
				r.Use(ownMerchant)
				r.Post("/catalogue-items", a.createCatalogueItem)
				r.Patch("/catalogue-items/{itemId}", a.updateCatalogueItem)
				r.Post("/jobs/{jobId}/ready", a.markReady)
			})
		})
	})
}

// ownMerchant allows the merchant account linked to {id}, or an admin.
func ownMerchant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.MustFromContext(r.Context()).ActsAs(auth.RoleMerchant, chi.URLParam(r, "id")) {
			httpx.Forbidden(w, "only this merchant's account (or an admin) can do this")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type merchantRequest struct {
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	OpensAt  string  `json:"opensAt"`  // "HH:MM", wall clock in Timezone
	ClosesAt string  `json:"closesAt"` // "HH:MM"; equal to opensAt means open all day
	Timezone string  `json:"timezone"` // IANA, e.g. "Africa/Windhoek"
}

func (a *API) createMerchant(w http.ResponseWriter, r *http.Request) {
	var req merchantRequest
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
	// Never guessed (guessing is how hours ended up judged in UTC, audit
	// P0-9): the merchant's own, or the one the brand is configured with.
	if req.Timezone == "" {
		req.Timezone = a.DefaultTimezone
	}
	if _, err := time.LoadLocation(req.Timezone); req.Timezone == "" || err != nil {
		httpx.BadRequest(w, "timezone must be an IANA zone such as Africa/Windhoek")
		return
	}
	rest := &domain.Merchant{
		ID: uuid.NewString(), Name: req.Name, Lat: req.Lat, Lng: req.Lng,
		OpensAt: sinceMidnight(opens), ClosesAt: sinceMidnight(closes), Timezone: req.Timezone, IsOpen: true,
	}
	if err := a.Store.CreateMerchant(r.Context(), rest); err != nil {
		a.Log.Error("create merchant", "error", err)
		httpx.Internal(w, "could not create merchant")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rest)
}

func sinceMidnight(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func (a *API) listMerchants(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListMerchants(r.Context())
	if err != nil {
		httpx.Internal(w, "could not list merchants")
		return
	}
	if list == nil {
		list = []*domain.Merchant{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) getMerchant(w http.ResponseWriter, r *http.Request) {
	rest, err := a.Store.GetMerchant(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "merchant not found")
			return
		}
		httpx.Internal(w, "could not load merchant")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rest)
}

type catalogueItemRequest struct {
	Name       string `json:"name"`
	PriceCents int64  `json:"priceCents"`
	StockQty   int    `json:"stockQty"`
	Available  bool   `json:"available"`
}

func (a *API) createCatalogueItem(w http.ResponseWriter, r *http.Request) {
	var req catalogueItemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" || req.PriceCents <= 0 || req.StockQty < 0 {
		httpx.BadRequest(w, "name, a positive priceCents and a non-negative stockQty are required")
		return
	}
	item := &domain.CatalogueItem{
		ID: uuid.NewString(), MerchantID: chi.URLParam(r, "id"),
		Name: req.Name, PriceCents: req.PriceCents, StockQty: req.StockQty, Available: req.Available,
	}
	if err := a.Store.CreateCatalogueItem(r.Context(), item); err != nil {
		a.Log.Error("create catalogue item", "error", err)
		httpx.Internal(w, "could not create catalogue item")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (a *API) listCatalogueItems(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListCatalogueItems(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, "could not list catalogue items")
		return
	}
	if list == nil {
		list = []*domain.CatalogueItem{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) updateCatalogueItem(w http.ResponseWriter, r *http.Request) {
	var req catalogueItemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	// Same rules as create: a zero or negative price would flow straight
	// into job totals and payment amounts.
	if req.Name == "" || req.PriceCents <= 0 || req.StockQty < 0 {
		httpx.BadRequest(w, "name, a positive priceCents and a non-negative stockQty are required")
		return
	}
	item := &domain.CatalogueItem{
		ID: chi.URLParam(r, "itemId"), MerchantID: chi.URLParam(r, "id"),
		Name: req.Name, PriceCents: req.PriceCents, StockQty: req.StockQty, Available: req.Available,
	}
	if err := a.Store.UpdateCatalogueItem(r.Context(), item); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "catalogue item not found")
			return
		}
		httpx.Internal(w, "could not update catalogue item")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

// markReady is called by merchant staff once the prep has finished
// preparing a confirmed job. It emits merchant.job-ready, which the
// jobs module uses to move to READY and the dispatch module uses to
// assign an agent.
func (a *API) markReady(w http.ResponseWriter, r *http.Request) {
	err := prep.MarkReady(r.Context(), a.Pool, chi.URLParam(r, "id"), chi.URLParam(r, "jobId"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "job not found")
	case errors.Is(err, prep.ErrNotPreparing):
		httpx.Conflict(w, "job is not being prepared yet (waiting for payment or already ready)")
	case err != nil:
		a.Log.Error("mark ready", "error", err)
		httpx.Internal(w, "could not mark job ready")
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": store.StatusReady})
	}
}
