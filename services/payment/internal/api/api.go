// Package api exposes a read-only view of payments for the gateway and
// admin dashboards.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/store"
)

type API struct {
	Store *store.Store
	Log   *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.With(a.Auth).Get("/payments/{jobId}", a.getPayment)
}

func (a *API) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := a.Store.GetByJob(r.Context(), chi.URLParam(r, "jobId"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "payment not found")
			return
		}
		a.Log.Error("get payment", "error", err)
		httpx.Internal(w, "could not load payment")
		return
	}
	// Payer or admin only. Checked after the load (a payment's owner is only
	// known from the row), and answered as 404 so ids can't be probed.
	caller := auth.MustFromContext(r.Context())
	if !caller.IsAdmin() && !(caller.Role == auth.RoleCustomer && caller.UserID == p.CustomerID) {
		httpx.NotFound(w, "payment not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
