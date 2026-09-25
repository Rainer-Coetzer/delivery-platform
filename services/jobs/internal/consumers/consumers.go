// Package consumers applies incoming events to jobs through the pack's
// lifecycle: an event either sets a fact, triggers a transition, or both
// (pkg/lifecycle decides). Two events carry data the lifecycle doesn't:
// dispatch.assigned records the agent, and dispatch.stop-completed marks
// the stop before it is run as a trigger. Every handler is idempotent
// (processed_events in the same transaction as the state change), so
// at-least-once delivery never applies an event twice.
package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

type Handlers struct {
	Pool      *db.Pool
	Log       *slog.Logger
	Lifecycle *lifecycle.Machine
}

// Topics is what the lifecycle reacts to, plus dispatch.assigned. A pack
// that never mentions an event doesn't get it delivered.
func (h *Handlers) Topics() []string {
	topics := h.Lifecycle.Events()
	if !h.Lifecycle.Reacts(events.DispatchAssigned) {
		topics = append(topics, events.DispatchAssigned)
	}
	return topics
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			h.Log.Info("duplicate event skipped", "eventId", env.EventID, "topic", env.EventType)
			return nil
		}
		j, err := store.GetForUpdate(ctx, tx, env.JobID)
		if err != nil {
			return fmt.Errorf("load job %s for %s: %w", env.JobID, env.EventType, err)
		}

		trigger := lifecycle.Trigger{Event: env.EventType}
		switch env.EventType {
		case events.DispatchAssigned:
			data, err := events.Decode[events.AssignedData](env)
			if err != nil {
				return err
			}
			j.AgentID = data.AgentID
		case events.DispatchStopCompleted:
			data, err := events.Decode[events.StopCompletedData](env)
			if err != nil {
				return err
			}
			if trigger.Stop, err = j.CompleteStop(data.StopSeq, env.Timestamp); err != nil {
				return bus.Permanent(err) // a stop the job doesn't have, or one already done: a retry can't fix it
			}
			if err := store.MarkStopCompleted(ctx, tx, j.ID, data.StopSeq, env.Timestamp); err != nil {
				return err
			}
		}

		var steps []lifecycle.Step
		if h.Lifecycle.Reacts(env.EventType) {
			steps, err = j.Apply(h.Lifecycle, trigger, reasonOf(env))
			if errors.Is(err, lifecycle.ErrNoTransition) && j.Terminal() {
				// A late event for a finished job (a payment landing after
				// a rejection cancelled it) is a normal race, not a fault.
				h.Log.Warn("event ignored: job already finished", "jobId", j.ID, "status", j.Status, "topic", env.EventType)
				return store.Save(ctx, tx, j, nil)
			}
			if err != nil {
				// On a live job this means the pack's lifecycle and the event
				// flow disagree. Retrying can't change that, so it goes
				// straight to the dead letters, where an operator sees it.
				err = fmt.Errorf("apply %s to job %s: %w", env.EventType, j.ID, err)
				if errors.Is(err, lifecycle.ErrNoTransition) {
					return bus.Permanent(err)
				}
				return err
			}
		}
		if err := store.Save(ctx, tx, j, steps); err != nil {
			return err
		}
		return PublishSteps(ctx, tx, j, steps)
	})
}

// PublishSteps writes one jobs.status-changed per step, plus jobs.cancelled
// if the job reached the cancelled milestone. The api package uses it too
// (role triggers, settling on creation).
func PublishSteps(ctx context.Context, tx pgx.Tx, j *domain.Job, steps []lifecycle.Step) error {
	for _, s := range steps {
		data := events.JobStatusChangedData{
			JobID: j.ID, CustomerID: j.CustomerID, MerchantID: j.MerchantID, AgentID: j.AgentID,
			From: s.From, To: s.To, ToMilestone: s.Milestone,
		}
		if s.Milestone == lifecycle.Cancelled {
			data.Reason = j.CancelReason
		}
		if s.Milestone == lifecycle.ReadyForDispatch {
			data.Stops = j.EventStops()
		}
		env, err := events.New(events.JobStatusChanged, j.ID, data)
		if err != nil {
			return err
		}
		if err := outbox.Insert(ctx, tx, env); err != nil {
			return err
		}
		if s.Milestone != lifecycle.Cancelled {
			continue
		}
		// jobs.cancelled carries the lines so the merchant can release
		// stock, and is what payment refunds on.
		lines := make([]events.JobLine, 0, len(j.Items))
		for _, it := range j.Items {
			lines = append(lines, events.JobLine{CatalogueItemID: it.CatalogueItemID, Name: it.Name, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
		}
		cancelled, err := events.New(events.JobCancelled, j.ID, events.JobCancelledData{
			JobID: j.ID, CustomerID: j.CustomerID, MerchantID: j.MerchantID, Items: lines, Reason: j.CancelReason,
		})
		if err != nil {
			return err
		}
		if err := outbox.Insert(ctx, tx, cancelled); err != nil {
			return err
		}
	}
	return nil
}

// reasonOf reads the optional "reason" every failure event carries
// (payments.failed, merchant.job-rejected), for the cancel reason.
func reasonOf(env events.Envelope) string {
	var v struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(env.Data, &v) // no reason is a fine answer
	return v.Reason
}
