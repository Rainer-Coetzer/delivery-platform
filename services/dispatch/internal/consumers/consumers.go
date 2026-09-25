// Package consumers assigns an agent as soon as a job reaches the
// ready_for_dispatch milestone (or queues it for retry if no agent is
// free), and releases the agent again if the job is cancelled. Which state
// that is, and what got it there (food: the merchant marking it ready), is
// the pack's business.
package consumers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/geo"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	dispatchstrategy "github.com/rainercoetzer/delivery-platform/pkg/strategy/dispatch"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/eta"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

// Topics this service consumes, kept next to the handlers that use them.
var Topics = []string{events.JobStatusChanged, events.JobCancelled}

type Handlers struct {
	Pool       *db.Pool
	Log        *slog.Logger
	Dispatcher *Dispatcher
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.JobStatusChanged:
		data, err := events.Decode[events.JobStatusChangedData](env)
		if err != nil {
			return err
		}
		if data.ToMilestone != lifecycle.ReadyForDispatch {
			return nil
		}
		route, err := RouteFor(data)
		if err != nil {
			return err
		}
		return h.handleReady(ctx, env, route)
	case events.JobCancelled:
		data, err := events.Decode[events.JobCancelledData](env)
		if err != nil {
			return err
		}
		return h.handleCancelled(ctx, env, data)
	default:
		return nil
	}
}

// RouteFor turns the stops the jobs module sends with ready_for_dispatch
// into a route: every stop in order, with the first pickup (where the
// agent is dispatched from) and the last stop called out.
func RouteFor(data events.JobStatusChangedData) (*store.Route, error) {
	if len(data.Stops) < 2 {
		return nil, fmt.Errorf("job %s reached ready_for_dispatch with %d stops; dispatch needs a pickup and a dropoff", data.JobID, len(data.Stops))
	}
	var pickup *events.Stop
	for i := range data.Stops {
		if data.Stops[i].Kind == events.StopPickup && (pickup == nil || data.Stops[i].Seq < pickup.Seq) {
			pickup = &data.Stops[i]
		}
	}
	if pickup == nil {
		return nil, fmt.Errorf("job %s has no pickup stop", data.JobID)
	}
	last := data.Stops[0]
	for _, s := range data.Stops {
		if s.Seq > last.Seq {
			last = s
		}
	}
	r := &store.Route{
		JobID: data.JobID, MerchantID: pickup.MerchantID,
		PickupLat: pickup.Location.Lat, PickupLng: pickup.Location.Lng, PickupSeq: pickup.Seq,
		DropoffLat: last.Location.Lat, DropoffLng: last.Location.Lng, DropoffSeq: last.Seq,
		Requires:   data.Requires,
		Stops:      data.Stops,
		CustomerID: data.CustomerID,
	}
	if data.Size != nil {
		r.WeightGrams, r.VolumeCm3 = data.Size.WeightGrams, data.Size.VolumeCm3
	}
	return r, nil
}

func (h *Handlers) handleReady(ctx context.Context, env events.Envelope, route *store.Route) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		// A booked job waits in the queue until its window (less the lead
		// time); the retrier picks it up then.
		if hold := h.Dispatcher.holdUntil(route); hold != nil && h.Dispatcher.now().Before(*hold) {
			route.NotBefore = hold
			h.Log.Info("booked job held until its window", "jobId", route.JobID, "notBefore", hold.Format(time.RFC3339))
			return store.InsertPending(ctx, tx, route)
		}
		outcome, err := h.Dispatcher.TryAssign(ctx, tx, route)
		if err != nil {
			return err
		}
		switch outcome {
		case Offered:
			h.Log.Info("job offered to agents", "jobId", route.JobID)
		case JobCancelled:
			h.Log.Info("ready for dispatch, but the job is cancelled: not dispatching", "jobId", route.JobID)
		case NoAgentFree:
			h.Log.Info("no agent available, queued for retry", "jobId", route.JobID)
			return store.InsertPending(ctx, tx, route)
		}
		return nil
	})
}

// handleCancelled tombstones the job, drops any queued assignment, and if
// an agent was already on it, cancels the assignment and puts the agent back
// in the pool (audit P0-10: before this, cancelled jobs kept agents BUSY
// forever and pending rows retried forever).
func (h *Handlers) handleCancelled(ctx context.Context, env events.Envelope, data events.JobCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		if err := store.LockJob(ctx, tx, data.JobID); err != nil {
			return err
		}
		if err := store.MarkJobCancelled(ctx, tx, data.JobID); err != nil {
			return err
		}
		if err := store.DeletePending(ctx, tx, data.JobID); err != nil {
			return err
		}

		d, err := store.GetByJobForUpdate(ctx, tx, data.JobID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // never dispatched: the tombstone and pending delete are all there is to do
		}
		if err != nil {
			return err
		}
		if d.Status != progress.StatusAssigned && d.Status != progress.StatusPickedUp {
			return nil // already completed or cancelled
		}
		if err := store.UpdateStatus(ctx, tx, d.ID, progress.StatusCancelled); err != nil {
			return err
		}
		h.Log.Info("assignment cancelled, agent released", "jobId", data.JobID, "agentId", d.AgentID)
		return store.MarkAgentAvailable(ctx, tx, d.AgentID)
	})
}

// AssignOutcome says what TryAssign did, so callers can queue, skip or
// clean up without guessing from a bool.
type AssignOutcome int

const (
	Assigned AssignOutcome = iota
	NoAgentFree
	JobCancelled
	Offered // open_offer: out to agents; the first to accept gets it
)

// Dispatcher assigns jobs with the pack's dispatch strategy and ETA model.
type Dispatcher struct {
	Strategy dispatchstrategy.Strategy
	ETA      eta.Estimator
	// Capacity is the pack's capacity module: only agents whose vehicle
	// fits the job, and has what it requires, are candidates.
	Capacity bool
	// Lead is how long before a booked pickup window opens dispatch
	// starts (the scheduling module's lead_minutes).
	Lead time.Duration
	// Now is the clock; tests set it.
	Now func() time.Time
}

// holdUntil is when a booked job may first be assigned, or nil for ASAP:
// its first pickup's window opening, less the lead time.
func (d *Dispatcher) holdUntil(r *store.Route) *time.Time {
	for _, s := range r.Stops {
		if s.Seq == r.PickupSeq && s.Window != nil {
			t := s.Window.Start.Add(-d.Lead)
			return &t
		}
	}
	return nil
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// maxCandidates bounds how many free agents a strategy chooses among: the
// nearest 20 is plenty for any sane radius, and keeps the query cheap.
const maxCandidates = 20

// TryAssign asks the strategy for an agent among the free ones nearest the
// pickup, claims it, creates the assignment, and inserts the
// dispatch.assigned outbox event. If the pick was taken by a concurrent
// assignment, it asks again without that agent. It never assigns a
// tombstoned (cancelled) job.
func (d *Dispatcher) TryAssign(ctx context.Context, tx pgx.Tx, r *store.Route) (AssignOutcome, error) {
	jobID := r.JobID
	if err := store.LockJob(ctx, tx, jobID); err != nil {
		return 0, err
	}
	cancelled, err := store.IsJobCancelled(ctx, tx, jobID)
	if err != nil {
		return 0, err
	}
	if cancelled {
		return JobCancelled, nil
	}

	if offerer, ok := d.Strategy.(dispatchstrategy.Offerer); ok {
		return d.offer(ctx, tx, r, offerer)
	}
	agent, err := d.claim(ctx, tx, r)
	if err != nil || agent == nil {
		return NoAgentFree, err
	}
	return Assigned, d.assign(ctx, tx, r, agent)
}

// ErrAgentNotAvailable: the chosen agent is busy, offline or locked by a
// concurrent assignment.
var ErrAgentNotAvailable = errors.New("agent is not available")

// AssignAgent hands a queued job to a chosen agent: an admin assigning by
// hand, or the agent who accepted its offer. The caller has locked the job
// (store.LockJob) and holds its queue row.
func (d *Dispatcher) AssignAgent(ctx context.Context, tx pgx.Tx, r *store.Route, agentID string) error {
	if err := store.LockAvailable(ctx, tx, agentID); errors.Is(err, store.ErrNotFound) {
		return ErrAgentNotAvailable
	} else if err != nil {
		return err
	}
	a, err := store.GetAgentTx(ctx, tx, agentID)
	if err != nil {
		return err
	}
	c := &store.Candidate{Agent: *a, DistanceMeters: geo.HaversineMeters(a.Lat, a.Lng, r.PickupLat, r.PickupLng)}
	if err := d.assign(ctx, tx, r, c); err != nil {
		return err
	}
	return store.DeletePending(ctx, tx, r.JobID) // takes the offers with it
}

// offer puts a job out to the nearest agents (the open_offer strategy):
// the job waits in the queue, held back from the retrier until the offers
// expire, and whoever accepts first gets it.
func (d *Dispatcher) offer(ctx context.Context, tx pgx.Tx, r *store.Route, o dispatchstrategy.Offerer) (AssignOutcome, error) {
	found, err := store.AvailableCandidates(ctx, tx, r, d.Capacity, maxCandidates)
	if err != nil {
		return 0, err
	}
	options := make([]dispatchstrategy.Candidate, len(found))
	for i, c := range found {
		options[i] = dispatchstrategy.Candidate{ID: c.ID, Vehicle: c.Vehicle, DistanceMeters: c.DistanceMeters, AssignmentsToday: c.AssignmentsToday,
			CapacityGrams: c.CapacityGrams, CapacityCm3: c.CapacityCm3}
	}
	to := o.Offer(ctx, dispatchstrategy.Request{JobID: r.JobID, WeightGrams: r.WeightGrams, VolumeCm3: r.VolumeCm3}, options)
	if len(to) == 0 {
		return NoAgentFree, nil
	}
	if err := store.InsertPending(ctx, tx, r); err != nil { // no-op if already queued
		return 0, err
	}
	ids := make([]string, len(to))
	for i, c := range to {
		ids[i] = c.ID
	}
	return Offered, store.SetOffers(ctx, tx, r.JobID, ids, d.now().Add(o.OfferFor()))
}

// assign creates the assignment for a claimed agent and announces it.
func (d *Dispatcher) assign(ctx context.Context, tx pgx.Tx, r *store.Route, agent *store.Candidate) error {
	jobID := r.JobID
	if err := store.MarkAgentBusy(ctx, tx, agent.ID); err != nil {
		return err
	}

	eta := d.ETA.Minutes(agent.DistanceMeters, agent.Vehicle)
	assignment := &store.Assignment{
		ID: uuid.NewString(), JobID: jobID, AgentID: agent.ID, MerchantID: r.MerchantID, CustomerID: r.CustomerID,
		PickupLat: r.PickupLat, PickupLng: r.PickupLng, DropoffLat: r.DropoffLat, DropoffLng: r.DropoffLng,
		PickupSeq: r.PickupSeq, DropoffSeq: r.DropoffSeq,
		Status: progress.StatusAssigned, EtaMinutes: eta,
	}
	if err := store.InsertAssignment(ctx, tx, assignment, r.Stops); err != nil {
		return err
	}

	env, err := events.New(events.DispatchAssigned, jobID, events.AssignedData{
		JobID: jobID, AssignmentID: assignment.ID, AgentID: agent.ID, AgentName: agent.Name,
		EtaMinutes: eta, AgentAt: events.Location{Lat: agent.Lat, Lng: agent.Lng},
	})
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}

// claim returns the locked agent the strategy picked, or nil if nobody
// suitable is free.
func (d *Dispatcher) claim(ctx context.Context, tx pgx.Tx, r *store.Route) (*store.Candidate, error) {
	found, err := store.AvailableCandidates(ctx, tx, r, d.Capacity, maxCandidates)
	if err != nil {
		return nil, err
	}
	for len(found) > 0 {
		options := make([]dispatchstrategy.Candidate, len(found))
		for i, c := range found {
			options[i] = dispatchstrategy.Candidate{ID: c.ID, Vehicle: c.Vehicle, DistanceMeters: c.DistanceMeters, AssignmentsToday: c.AssignmentsToday,
				CapacityGrams: c.CapacityGrams, CapacityCm3: c.CapacityCm3}
		}
		pick, ok := d.Strategy.Pick(ctx, dispatchstrategy.Request{JobID: r.JobID, WeightGrams: r.WeightGrams, VolumeCm3: r.VolumeCm3}, options)
		if !ok {
			return nil, nil
		}
		i := slices.IndexFunc(found, func(c store.Candidate) bool { return c.ID == pick.ID })
		if i < 0 {
			return nil, fmt.Errorf("dispatch strategy picked %q, which wasn't a candidate", pick.ID)
		}
		err := store.LockAvailable(ctx, tx, pick.ID)
		if err == nil {
			return &found[i], nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		found = slices.Delete(found, i, i+1) // taken meanwhile: choose again without them
	}
	return nil, nil
}
