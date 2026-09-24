// Package api exposes a notification inbox: list by recipient and mark
// read.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/store"
)

type API struct {
	Store *store.Store
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/notifications", func(r chi.Router) {
		r.Use(a.Auth)
		r.Get("/", a.list)
		r.Patch("/{id}/read", a.markRead)
	})
}

// MayRead reports whether caller may read the inbox named by recipient:
// their own user inbox, their restaurant's or driver's inbox via refId, or
// anything for an admin. "order:<id>" inboxes are admin-only until Phase 4
// replaces that recipient scheme (audit P1: nothing could read them anyway).
func MayRead(caller *auth.Claims, recipient string) bool {
	switch {
	case caller.IsAdmin():
		return true
	case recipient == "user:"+caller.UserID:
		return true
	case caller.RefID == "":
		return false
	case recipient == "restaurant:"+caller.RefID:
		return caller.Role == auth.RoleRestaurant
	case recipient == "driver:"+caller.RefID:
		return caller.Role == auth.RoleDriver
	}
	return false
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	recipient := r.URL.Query().Get("recipient")
	if recipient == "" {
		httpx.BadRequest(w, "recipient query parameter is required, e.g. user:<id> or restaurant:<id>")
		return
	}
	if !MayRead(auth.MustFromContext(r.Context()), recipient) {
		httpx.Forbidden(w, "not your inbox")
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
	recipient, err := a.Store.Recipient(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !MayRead(auth.MustFromContext(r.Context()), recipient) {
		httpx.NotFound(w, "notification not found") // 404 either way: ids can't be probed
		return
	}
	if err := a.Store.MarkRead(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpx.Internal(w, "could not mark notification read")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "read"})
}
