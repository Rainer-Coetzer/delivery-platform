// Package api exposes account registration and verification (verify is
// called by the gateway during login), admin account creation, and the
// customer profile, address and job-history endpoints.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/store"
)

type API struct {
	Store *store.Store
	Log   *slog.Logger
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
	// Roles are the account kinds this platform has (the brand's roles);
	// nil means all four.
	Roles []string
}

func (a *API) offers(role auth.Role) bool {
	return a.Roles == nil || slices.Contains(a.Roles, string(role))
}

// Routes: registration and verify are public, everything else needs a
// token. Ownership is checked per handler.
func (a *API) Routes(r chi.Router) {
	// Flat routes, not r.Route("/auth"): in the platform binary every module
	// shares one router, and a mounted /auth subrouter would swallow the
	// gateway's /auth/login.
	r.Post("/auth/register", a.register)
	// verify is only for the gateway's login handler. The gateway never
	// routes it publicly and backends have no host ports; it is no more
	// powerful than /auth/login itself.
	r.Post("/auth/verify", a.verify)
	r.Group(func(r chi.Router) {
		r.Use(a.Auth)
		r.With(auth.RequireRole(auth.RoleAdmin)).Post("/users", a.createUser)
		r.Route("/customers/{id}", func(r chi.Router) {
			r.Use(selfOrAdmin)
			r.Get("/", a.getProfile)
			r.Post("/addresses", a.addAddress)
			r.Get("/addresses", a.listAddresses)
			r.Get("/jobs", a.jobHistory)
		})
	})
}

// selfOrAdmin lets a user reach only their own /customers/{id} subtree.
func selfOrAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := auth.MustFromContext(r.Context())
		if !c.IsAdmin() && c.UserID != chi.URLParam(r, "id") {
			httpx.Forbidden(w, "you can only access your own account")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Role and RefID are accepted only so a client that still sends them
	// gets a clear 400 instead of "unknown field". Public registration
	// always creates a customer (audit P0-3: anyone could register as admin).
	Role  string `json:"role"`
	RefID string `json:"refId"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	if req.Role != "" && req.Role != string(auth.RoleCustomer) {
		httpx.BadRequest(w, "public registration creates customer accounts only; merchant, agent and admin accounts are created by an admin")
		return
	}
	if req.RefID != "" {
		httpx.BadRequest(w, "refId can't be set on registration")
		return
	}
	if !a.offers(auth.RoleCustomer) {
		httpx.Forbidden(w, "this platform has no customer sign-up")
		return
	}
	a.create(w, r, req.Name, req.Email, req.Password, auth.RoleCustomer, "")
}

type createUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	RefID    string `json:"refId"`
}

// createUser is the admin onboarding path: it creates a merchant or agent
// account already linked (refId) to the merchant/agent the admin created
// in that service, or another admin.
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}
	role := auth.Role(req.Role)
	if !auth.ValidRole(role) {
		httpx.BadRequest(w, "role must be customer, merchant, agent or admin")
		return
	}
	if !a.offers(role) {
		httpx.BadRequest(w, "this platform has no "+string(role)+" accounts (brand roles)")
		return
	}
	needsRef := role == auth.RoleMerchant || role == auth.RoleAgent
	if needsRef && req.RefID == "" {
		httpx.BadRequest(w, "merchant and agent accounts need refId (the merchant or agent id)")
		return
	}
	if !needsRef && req.RefID != "" {
		httpx.BadRequest(w, "refId only applies to merchant and agent accounts")
		return
	}
	a.create(w, r, req.Name, req.Email, req.Password, role, req.RefID)
}

func (a *API) create(w http.ResponseWriter, r *http.Request, name, email, password string, role auth.Role, refID string) {
	if name == "" || email == "" || len(password) < 8 {
		httpx.BadRequest(w, "name, email and a password of at least 8 characters are required")
		return
	}
	u, err := NewUser(name, email, password, role, refID)
	if err != nil {
		httpx.Internal(w, "could not hash password")
		return
	}
	if err := a.Store.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrDuplicateEmail) {
			httpx.Conflict(w, "email already registered")
			return
		}
		a.Log.Error("create user", "role", role, "error", err)
		httpx.Internal(w, "could not create account")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, publicUser(u))
}

// NewUser hashes the password and builds a user row. Also used by the
// bootstrap admin at startup.
func NewUser(name, email, password string, role auth.Role, refID string) (*store.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &store.User{ID: uuid.NewString(), Name: name, Email: email, PasswordHash: string(hash), Role: string(role), RefID: refID}, nil
}

type verifyRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// dummyHash is compared against when the email doesn't exist, so a missing
// account takes as long as a wrong password. Without it, response time
// tells an attacker which emails are registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equaliser"), bcrypt.DefaultCost)

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
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
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

func (a *API) jobHistory(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListJobHistory(r.Context(), chi.URLParam(r, "id"), 100)
	if err != nil {
		httpx.Internal(w, "could not load job history")
		return
	}
	if list == nil {
		list = []*store.JobHistoryEntry{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
