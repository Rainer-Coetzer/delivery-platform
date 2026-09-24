// Package api exposes account registration/verification (used internally by
// the gateway to issue JWTs) and customer profile, address and order-history
// endpoints.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/store"
)

type API struct {
	Store *store.Store
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Get("/healthz", httpx.Health)
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", a.register)
		r.Post("/verify", a.verify) // internal: called by the gateway during login
	})
	r.Route("/customers", func(r chi.Router) {
		r.Get("/{id}", a.getProfile)
		r.Post("/{id}/addresses", a.addAddress)
		r.Get("/{id}/addresses", a.listAddresses)
		r.Get("/{id}/orders", a.orderHistory)
	})
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`  // customer, restaurant, driver, admin
	RefID    string `json:"refId"` // restaurantId/driverId, set once that entity exists
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" || req.Email == "" || len(req.Password) < 8 {
		httpx.BadRequest(w, "name, email and a password of at least 8 characters are required")
		return
	}
	if req.Role == "" {
		req.Role = "customer"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Internal(w, "could not hash password")
		return
	}
	u := &store.User{ID: uuid.NewString(), Name: req.Name, Email: req.Email, PasswordHash: string(hash), Role: req.Role, RefID: req.RefID}
	if err := a.Store.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrDuplicateEmail) {
			httpx.Conflict(w, "email already registered")
			return
		}
		a.Log.Error("register", "error", err)
		httpx.Internal(w, "could not register")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, publicUser(u))
}

type verifyRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// verify checks credentials and returns the profile the gateway signs into
// a JWT. It never returns the password hash.
func (a *API) verify(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	u, err := a.Store.GetByEmail(r.Context(), req.Email)
	if err != nil {
		httpx.Unauthorized(w, "invalid email or password")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		httpx.Unauthorized(w, "invalid email or password")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, publicUser(u))
}

func publicUser(u *store.User) map[string]string {
	return map[string]string{"id": u.ID, "name": u.Name, "email": u.Email, "role": u.Role, "refId": u.RefID}
}

func (a *API) getProfile(w http.ResponseWriter, r *http.Request) {
	u, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "user not found")
			return
		}
		httpx.Internal(w, "could not load profile")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, publicUser(u))
}

type addressRequest struct {
	Label string  `json:"label"`
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
}

func (a *API) addAddress(w http.ResponseWriter, r *http.Request) {
	var req addressRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Label == "" {
		httpx.BadRequest(w, "label is required")
		return
	}
	addr := &store.Address{ID: uuid.NewString(), UserID: chi.URLParam(r, "id"), Label: req.Label, Lat: req.Lat, Lng: req.Lng}
	if err := a.Store.AddAddress(r.Context(), addr); err != nil {
		httpx.Internal(w, "could not add address")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, addr)
}

func (a *API) listAddresses(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListAddresses(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, "could not list addresses")
		return
	}
	if list == nil {
		list = []*store.Address{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (a *API) orderHistory(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListOrderHistory(r.Context(), chi.URLParam(r, "id"), 100)
	if err != nil {
		httpx.Internal(w, "could not load order history")
		return
	}
	if list == nil {
		list = []*store.OrderHistoryEntry{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
