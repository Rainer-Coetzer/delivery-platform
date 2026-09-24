// Package api exposes a read-only view of payments for the gateway and
// admin dashboards.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/store"
)

type API struct {
	Store *store.Store
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Get("/payments/{orderId}", a.getPayment)
}

func (a *API) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := a.Store.GetByOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "payment not found")
			return
		}
		a.Log.Error("get payment", "error", err)
		httpx.Internal(w, "could not load payment")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
