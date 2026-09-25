// Package consumers assigns an agent as soon as a merchant marks a job
// ready (or queues the job for retry if no agent is free), and releases
// the agent again if the job is cancelled.
package consumers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

// Topics this service consumes, kept next to the handlers that use them.
var Topics = []string{events.MerchantReady, events.JobCancelled}

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.MerchantReady:
		data, err := events.Decode[events.MerchantReadyData](env)
		if err != nil {
			return err
		}
		return h.handleReady(ctx, env, data)
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

func (h *Handlers) handleReady(ctx context.Context, env events.Envelope, data events.MerchantReadyData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		outcome, err := TryAssign(ctx, tx, data.JobID, data.MerchantID, data.Pickup, data.Dropoff)
		if err != nil {
			return err
		}
		switch outcome {
		case JobCancelled:
			h.Log.Info("job-ready for a cancelled job, not dispatching", "jobId", data.JobID)
		case NoAgentFree:
			h.Log.Info("no agent available, queued for retry", "jobId", data.JobID)
			return store.InsertPending(ctx, tx, &store.PendingAssignment{
				JobID: data.JobID, MerchantID: data.MerchantID,
				PickupLat: data.Pickup.Lat, PickupLng: data.Pickup.Lng,
				DropoffLat: data.Dropoff.Lat, DropoffLng: data.Dropoff.Lng,
			})
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
)

// TryAssign locks and claims the nearest AVAILABLE agent to the pickup
// point, creates the assignment, and inserts the dispatch.assigned outbox
// event. It never assigns a tombstoned (cancelled) job.
func TryAssign(ctx context.Context, tx pgx.Tx, jobID, merchantID string, pickup, dropoff events.Location) (AssignOutcome, error) {
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

	agent, err := store.FindNearestAvailableForUpdate(ctx, tx, pickup.Lat, pickup.Lng)
	if errors.Is(err, store.ErrNotFound) {
		return NoAgentFree, nil
	}
	if err != nil {
		return 0, err
	}
	if err := store.MarkAgentBusy(ctx, tx, agent.ID); err != nil {
		return 0, err
	}

	eta := domain.ETAMinutes(domain.HaversineMeters(agent.Lat, agent.Lng, pickup.Lat, pickup.Lng))
	assignment := &store.Assignment{
		ID: uuid.NewString(), JobID: jobID, AgentID: agent.ID, MerchantID: merchantID,
		PickupLat: pickup.Lat, PickupLng: pickup.Lng, DropoffLat: dropoff.Lat, DropoffLng: dropoff.Lng,
		Status: progress.StatusAssigned, EtaMinutes: eta,
	}
	if err := store.InsertAssignment(ctx, tx, assignment); err != nil {
		return 0, err
	}

	env, err := events.New(events.DispatchAssigned, jobID, events.AssignedData{
		JobID: jobID, AssignmentID: assignment.ID, AgentID: agent.ID, AgentName: agent.Name,
		EtaMinutes: eta, AgentAt: events.Location{Lat: agent.Lat, Lng: agent.Lng},
	})
	if err != nil {
		return 0, err
	}
	if err := outbox.Insert(ctx, tx, env); err != nil {
		return 0, err
	}
	return Assigned, nil
}
