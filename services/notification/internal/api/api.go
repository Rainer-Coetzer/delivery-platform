// Package api exposes a notification inbox: list by recipient and mark
// read.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/store"
)

type API struct {
	Store *store.Store
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Get("/notifications", a.list)
	r.Patch("/notifications/{id}/read", a.markRead)
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	recipient := r.URL.Query().Get("recipient")
	if recipient == "" {
		httpx.BadRequest(w, "recipient query parameter is required, e.g. user:<id> or restaurant:<id>")
		return
	}
	list, err := a.Store.ListFor(r.Context(), recipient, 100)
	if err != nil {
		httpx.Internal(w, "could not list notifications")
		return
	}
	if list == nil {
		list = []*store.Notification{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) markRead(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.MarkRead(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpx.Internal(w, "could not mark notification read")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "read"})
}
