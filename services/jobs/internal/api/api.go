// Package api exposes the jobs module's REST endpoints.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/payment"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/pricing"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/catalogue"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

// Catalogue is the merchant lookup createJob prices from;
// *catalogue.Client in production, a fake in tests.
type Catalogue interface {
	Fetch(ctx context.Context, authorization, merchantID string) (*catalogue.Merchant, map[string]domain.CatalogueItem, error)
}

type API struct {
	Pool      *db.Pool
	Store     *store.Store
	Catalogue Catalogue
	Log       *slog.Logger
	Pack      *packs.Pack
	Pricing   pricing.Policy
	Payments  *payment.Set
	// Auth is auth.Issuer.Middleware; injected so tests can swap it.
	Auth func(http.Handler) http.Handler
}

func (a *API) Routes(r chi.Router) {
	r.Route("/jobs", func(r chi.Router) {
		r.Use(a.Auth)
		r.With(auth.RequireRole(auth.RoleCustomer)).Post("/", a.createJob)
		r.Get("/{id}", a.getJob)
		r.Post("/{id}/transitions", a.transition)
		r.Post("/{id}/facts/{fact}", a.setFact)
		r.Get("/", a.listJobs)
	})
}

type requestedItem struct {
	CatalogueItemID string `json:"catalogueItemId"`
	Quantity        int    `json:"quantity"`
	// Name and UnitPriceCents are accepted only so older clients don't get
	// "unknown field" errors. They are ignored: prices and names come from
	// the merchant's catalogue (audit P0-4).
	Name           string `json:"name,omitempty"`
	UnitPriceCents int64  `json:"unitPriceCents,omitempty"`
}

type createJobRequest struct {
	MerchantID    string          `json:"merchantId"`
	Items         []requestedItem `json:"items"`
	PaymentMethod string          `json:"paymentMethod"`
	Dropoff       events.Location `json:"dropoff"`

	// Ignored, for the same compatibility reason: the customer is whoever
	// the token says, and pickup is the merchant's own location.
	CustomerID   string          `json:"customerId,omitempty"`
	CustomerName string          `json:"customerName,omitempty"`
	MerchantName string          `json:"merchantName,omitempty"`
	Pickup       events.Location `json:"pickup,omitempty"`
}

// createJob prices the job from the merchant's catalogue, persists it
// with its outbox row in one transaction, and returns 201. The outbox relay
// then publishes jobs.created, and merchant and payment race to confirm
// it (topics in pkg/events).
func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	var req createJobRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}
	// Other stop templates (courier's point_to_point, freight's multi_stop)
	// arrive with their packs; until then, say so rather than guess.
	if a.Pack.Job.Stops != packs.StopsMerchantToCustomer {
		httpx.WriteError(w, http.StatusNotImplemented, "not_implemented",
			"pack "+a.Pack.Name+" uses stops: "+a.Pack.Job.Stops+", which job creation doesn't support yet")
		return
	}
	if req.MerchantID == "" || req.PaymentMethod == "" {
		httpx.BadRequest(w, "merchantId and paymentMethod are required")
		return
	}
	if _, ok := a.Payments.For(req.PaymentMethod); !ok {
		httpx.BadRequest(w, "paymentMethod must be one of: "+strings.Join(a.Payments.Methods(), ", "))
		return
	}
	if !validDropoff(req.Dropoff) {
		httpx.BadRequest(w, "dropoff must be a real coordinate (lat -90..90, lng -180..180)")
		return
	}

	ctx := r.Context()
	rest, offered, err := a.Catalogue.Fetch(ctx, r.Header.Get("Authorization"), req.MerchantID)
	if errors.Is(err, catalogue.ErrMerchantNotFound) {
		httpx.BadRequest(w, "merchant not found")
		return
	}
	if err != nil {
		a.Log.Error("fetch catalogue", "merchantId", req.MerchantID, "error", err)
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "could not load the merchant's catalogue, try again")
		return
	}

	lines := make([]domain.RequestedLine, 0, len(req.Items))
	for _, it := range req.Items {
		lines = append(lines, domain.RequestedLine{CatalogueItemID: it.CatalogueItemID, Quantity: it.Quantity})
	}
	items, subtotal, err := domain.PriceItems(lines, offered)
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	stops := []domain.Stop{
		{Stop: events.Stop{Seq: 0, Kind: events.StopPickup, MerchantID: rest.ID,
			Location: events.Location{Lat: rest.Lat, Lng: rest.Lng, Label: rest.Name}}},
		{Stop: events.Stop{Seq: 1, Kind: events.StopDropoff, Location: req.Dropoff}},
	}

	openJobs, err := a.Store.CountOpen(ctx, req.MerchantID)
	if err != nil {
		a.Log.Error("count open jobs", "error", err)
		httpx.Internal(w, "could not price job")
		return
	}
	q, err := a.Pricing.Price(ctx, pricing.Input{
		SubtotalCents: subtotal,
		Stops:         (&domain.Job{Stops: stops}).EventStops(),
		// Known debt (audit P1): the load is one merchant's open jobs over a
		// fixed agent estimate. The jobs module doesn't know how many agents
		// are free; dispatch does, and doesn't publish it yet.
		Load: pricing.Load{OpenJobs: openJobs, FreeAgents: assumedFreeAgents},
	})
	if errors.Is(err, pricing.ErrNoFare) {
		httpx.BadRequest(w, err.Error())
		return
	}
	if err != nil {
		a.Log.Error("price job", "error", err)
		httpx.Internal(w, "could not price job")
		return
	}

	m := a.Pack.Lifecycle
	job := &domain.Job{
		ID:               uuid.NewString(),
		Pack:             a.Pack.Name,
		CustomerID:       caller.UserID,
		CustomerName:     caller.Name,
		MerchantID:       rest.ID,
		MerchantName:     rest.Name,
		Status:           m.Initial,
		Milestone:        lifecycle.Created,
		Facts:            map[string]bool{},
		Items:            items,
		SubtotalCents:    q.SubtotalCents,
		DeliveryFeeCents: q.FeeCents,
		SurgeMultiplier:  float64(q.MultiplierPct) / 100, // display only; the fee is integer cents
		TotalCents:       q.TotalCents,
		PaymentMethod:    req.PaymentMethod,
		Stops:            stops,
	}

	eventItems := make([]events.JobLine, 0, len(items))
	for _, it := range items {
		eventItems = append(eventItems, events.JobLine{CatalogueItemID: it.CatalogueItemID, Name: it.Name, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
	}
	env, err := events.New(events.JobCreated, job.ID, events.JobCreatedData{
		JobID: job.ID, CustomerID: job.CustomerID, CustomerName: job.CustomerName,
		MerchantID: job.MerchantID, MerchantName: job.MerchantName,
		Items: eventItems, SubtotalCents: job.SubtotalCents, DeliveryFeeCents: job.DeliveryFeeCents,
		SurgeMultiplier: job.SurgeMultiplier, TotalCents: job.TotalCents, PaymentMethod: req.PaymentMethod,
		Pack: job.Pack, State: job.Status, Stops: job.EventStops(),
	})
	if err != nil {
		httpx.Internal(w, "could not build event")
		return
	}
	// jobs.created describes the job as created; settling (a pack whose
	// initial state moves on by itself) follows as status changes.
	settled := job.Settle(m)

	err = a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		if err := store.Create(ctx, tx, job, m.Initial, settled); err != nil {
			return err
		}
		if err := outbox.Insert(ctx, tx, env); err != nil {
			return err
		}
		return consumers.PublishSteps(ctx, tx, job, settled)
	})
	if err != nil {
		a.Log.Error("create job", "error", err)
		httpx.Internal(w, "could not create job")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, job)
}

// assumedFreeAgents stands in for the real count of free agents in the
// surge load until dispatch publishes it.
const assumedFreeAgents = 5

// validDropoff rejects out-of-range coordinates and the 0,0 an omitted
// dropoff decodes to (a point in the Atlantic that would send an agent
// nowhere useful).
func validDropoff(l events.Location) bool {
	if l.Lat == 0 && l.Lng == 0 {
		return false
	}
	return l.Lat >= -90 && l.Lat <= 90 && l.Lng >= -180 && l.Lng <= 180
}

// CanView reports whether caller may see job o: its customer, its
// merchant, its assigned agent, or an admin. The gateway's WebSocket
// relies on this too: it asks GET /jobs/{id} as the user before letting
// them watch a job live.
func CanView(caller *auth.Claims, o *domain.Job) bool {
	switch {
	case caller.IsAdmin():
		return true
	case caller.Role == auth.RoleCustomer:
		return caller.UserID == o.CustomerID
	case caller.Role == auth.RoleMerchant:
		return caller.ActsAs(auth.RoleMerchant, o.MerchantID)
	case caller.Role == auth.RoleAgent:
		return o.AgentID != "" && caller.ActsAs(auth.RoleAgent, o.AgentID)
	}
	return false
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.NotFound(w, "job not found")
			return
		}
		a.Log.Error("get job", "error", err)
		httpx.Internal(w, "could not load job")
		return
	}
	caller := auth.MustFromContext(r.Context())
	if !CanView(caller, job) {
		httpx.Forbidden(w, "this isn't your job")
		return
	}
	a.fillActions(job, caller)
	httpx.WriteJSON(w, http.StatusOK, job)
}

// fillActions tells the caller what they may do with the job next.
func (a *API) fillActions(job *domain.Job, caller *auth.Claims) {
	if job.Terminal() {
		return
	}
	job.Actions = a.Pack.Lifecycle.RoleTargets(job.Status, string(caller.Role))
	if a.Pack.Has(packs.ModApproval) {
		job.CanSet = a.Pack.Lifecycle.RoleFacts(job.Facts, string(caller.Role))
	}
}

type transitionRequest struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// transition is a person moving a job: a customer cancelling, an admin
// stepping in. Who may make which move is the pack's lifecycle (`by:`
// roles); on top of that the caller must be someone who can see the job,
// so a merchant can't cancel another merchant's job.
func (a *API) transition(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	var req transitionRequest
	if err := httpx.Decode(r, &req); err != nil || req.To == "" {
		httpx.BadRequest(w, `body must be {"to": "<state>", "reason": "..."}`)
		return
	}
	a.personMove(w, r, caller, func(job *domain.Job) ([]lifecycle.Step, string, error) {
		steps, err := job.Apply(a.Pack.Lifecycle, lifecycle.Trigger{Role: string(caller.Role), To: req.To}, req.Reason)
		return steps, "a " + string(caller.Role) + " can't move this job from " + job.Status + " to " + req.To, err
	})
}

// setFact is the approval module: a person vouching for a fact the pack's
// lifecycle lets their role set (a pharmacist approving an order, an agent
// confirming an ID check). Setting it may unlock automatic transitions.
func (a *API) setFact(w http.ResponseWriter, r *http.Request) {
	if !a.Pack.Has(packs.ModApproval) {
		httpx.NotFound(w, "this platform has no approvals")
		return
	}
	caller := auth.MustFromContext(r.Context())
	fact := chi.URLParam(r, "fact")
	a.personMove(w, r, caller, func(job *domain.Job) ([]lifecycle.Step, string, error) {
		if job.Terminal() {
			return nil, "this job has finished", lifecycle.ErrNoTransition
		}
		if job.Facts[fact] {
			return nil, fact + " is already set", lifecycle.ErrNoTransition
		}
		steps, err := job.SetFact(a.Pack.Lifecycle, fact, string(caller.Role))
		return steps, "a " + string(caller.Role) + " can't set " + fact, err
	})
}

// personMove loads the job for update, checks the caller may see it, runs
// move, and saves and publishes whatever it did. move returns the message
// to answer with if the lifecycle refuses (409).
func (a *API) personMove(w http.ResponseWriter, r *http.Request, caller *auth.Claims, move func(*domain.Job) ([]lifecycle.Step, string, error)) {
	ctx := r.Context()
	var job *domain.Job
	var refusal string
	err := a.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if job, err = store.GetForUpdate(ctx, tx, chi.URLParam(r, "id")); err != nil {
			return err
		}
		if !CanView(caller, job) {
			return errNotYours
		}
		var steps []lifecycle.Step
		if steps, refusal, err = move(job); err != nil {
			return err
		}
		if err := store.Save(ctx, tx, job, steps); err != nil {
			return err
		}
		return consumers.PublishSteps(ctx, tx, job, steps)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w, "job not found")
	case errors.Is(err, errNotYours):
		httpx.Forbidden(w, "this isn't your job")
	case errors.Is(err, lifecycle.ErrNoTransition):
		httpx.WriteError(w, http.StatusConflict, "not_allowed", refusal)
	case err != nil:
		a.Log.Error("update job", "path", r.URL.Path, "error", err)
		httpx.Internal(w, "could not update job")
	default:
		a.fillActions(job, caller)
		httpx.WriteJSON(w, http.StatusOK, job)
	}
}

var errNotYours = errors.New("caller may not see this job")

// listJobs: customers always get their own jobs (a customerId param for
// anyone else is refused, not silently replaced), merchants only their
// own merchantId, admins anything.
func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	caller := auth.MustFromContext(r.Context())
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	customerID := r.URL.Query().Get("customerId")
	merchantID := r.URL.Query().Get("merchantId")

	if caller.Role == auth.RoleCustomer {
		if customerID != "" && customerID != caller.UserID {
			httpx.Forbidden(w, "you can only list your own jobs")
			return
		}
		customerID, merchantID = caller.UserID, ""
	}
	if merchantID != "" && !caller.ActsAs(auth.RoleMerchant, merchantID) {
		httpx.Forbidden(w, "you can only list your own merchant's jobs")
		return
	}
	if customerID != "" && !caller.IsAdmin() && customerID != caller.UserID {
		httpx.Forbidden(w, "you can only list your own jobs")
		return
	}

	var (
		jobs []*domain.Job
		err  error
	)
	switch {
	case customerID != "":
		jobs, err = a.Store.ListByCustomer(r.Context(), customerID, limit)
	case merchantID != "":
		jobs, err = a.Store.ListByMerchant(r.Context(), merchantID, limit)
	default:
		httpx.BadRequest(w, "customerId or merchantId query parameter is required")
		return
	}
	if err != nil {
		a.Log.Error("list jobs", "error", err)
		httpx.Internal(w, "could not list jobs")
		return
	}
	if jobs == nil {
		jobs = []*domain.Job{}
	}
	httpx.WriteJSON(w, http.StatusOK, jobs)
}
