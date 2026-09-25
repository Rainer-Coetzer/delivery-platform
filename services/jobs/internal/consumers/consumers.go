// Package consumers applies incoming Kafka events to the Job aggregate:
// merchant decisions, payment outcomes and delivery progress. Every
// handler is idempotent (checked against processed_events in the same
// transaction as the state change) so at-least-once delivery from Kafka
// never double-applies an event.
package consumers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

// Handle dispatches env to the right handler by topic. Unknown topics are
// logged and dropped rather than dead-lettered, since a service subscribing
// to a superset of topics it cares about is normal.
func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.MerchantAccepted:
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			o.MerchantAccepted = true
			return "", nil
		})
	case events.MerchantRejected:
		data, err := events.Decode[events.MerchantDecisionData](env)
		if err != nil {
			return err
		}
		return h.cancel(ctx, env, data.Reason)
	case events.MerchantReady:
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			return o.Transition(events.StatusReady, "")
		})
	case events.PaymentCompleted:
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			o.Paid = true
			return "", nil
		})
	case events.PaymentFailed:
		data, err := events.Decode[events.PaymentData](env)
		if err != nil {
			return err
		}
		return h.cancel(ctx, env, data.Reason)
	case events.DispatchAssigned:
		data, err := events.Decode[events.AssignedData](env)
		if err != nil {
			return err
		}
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			o.AgentID = data.AgentID
			return "", nil
		})
	case events.DispatchPickedUp:
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			return o.Transition(events.StatusOutForDelivery, "")
		})
	case events.DispatchCompleted:
		return h.withJob(ctx, env, func(o *domain.Job) (string, error) {
			return o.Transition(events.StatusDelivered, "")
		})
	default:
		h.Log.Debug("ignoring unhandled topic", "topic", env.EventType)
		return nil
	}
}

// withJob loads the job for update, applies mutate, persists any status
// change plus flag changes, tries MaybeConfirm, and inserts the resulting
// outbox events — all inside one transaction guarded by the idempotency
// check. mutate may change flags without transitioning (return "" as the
// "from" status) or perform its own explicit Transition.
func (h *Handlers) withJob(ctx context.Context, env events.Envelope, mutate func(o *domain.Job) (from string, err error)) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			h.Log.Info("duplicate event skipped", "eventId", env.EventID, "topic", env.EventType)
			return nil
		}

		jobID := jobIDFor(env)
		o, err := store.GetForUpdate(ctx, tx, jobID)
		if err != nil {
			return fmt.Errorf("load job %s: %w", jobID, err)
		}

		explicitFrom, err := mutate(o)
		if err != nil {
			return fmt.Errorf("apply event %s to job %s: %w", env.EventType, jobID, err)
		}

		if explicitFrom != "" {
			if err := store.ApplyTransition(ctx, tx, o, explicitFrom); err != nil {
				return err
			}
			if err := insertStatusChanged(ctx, tx, o, explicitFrom); err != nil {
				return err
			}
		} else if err := store.UpdateFlags(ctx, tx, o); err != nil {
			return err
		}

		confirmed, from, err := o.MaybeConfirm()
		if err != nil {
			return fmt.Errorf("auto-confirm job %s: %w", jobID, err)
		}
		if confirmed {
			if err := store.ApplyTransition(ctx, tx, o, from); err != nil {
				return err
			}
			if err := insertStatusChanged(ctx, tx, o, from); err != nil {
				return err
			}
		}
		return nil
	})
}

// cancel transitions the job to CANCELLED and emits both
// jobs.status-changed and jobs.cancelled — the latter carries the line
// items so the merchant can release stock and payment can issue a refund.
func (h *Handlers) cancel(ctx context.Context, env events.Envelope, reason string) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		jobID := jobIDFor(env)
		o, err := store.GetForUpdate(ctx, tx, jobID)
		if err != nil {
			return fmt.Errorf("load job %s: %w", jobID, err)
		}
		from, err := o.Transition(events.StatusCancelled, reason)
		if err != nil {
			// Already terminal (e.g. two failure events racing) — safe to ignore.
			h.Log.Warn("cancel ignored", "jobId", jobID, "status", o.Status, "error", err)
			return nil
		}
		if err := store.ApplyTransition(ctx, tx, o, from); err != nil {
			return err
		}
		if err := insertStatusChanged(ctx, tx, o, from); err != nil {
			return err
		}

		var items []events.JobLine
		for _, it := range o.Items {
			items = append(items, events.JobLine{CatalogueItemID: it.CatalogueItemID, Name: it.Name, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
		}
		cancelledEnv, err := events.New(events.JobCancelled, o.ID, events.JobCancelledData{
			JobID: o.ID, CustomerID: o.CustomerID, MerchantID: o.MerchantID, Items: items, Reason: reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, cancelledEnv)
	})
}

func insertStatusChanged(ctx context.Context, tx pgx.Tx, o *domain.Job, from string) error {
	env, err := events.New(events.JobStatusChanged, o.ID, events.JobStatusChangedData{
		JobID: o.ID, CustomerID: o.CustomerID, MerchantID: o.MerchantID, AgentID: o.AgentID,
		From: from, To: o.Status, Reason: o.CancelReason,
	})
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}

// jobIDFor reads the jobId every consumed event carries either at the
// envelope level or inside its data payload; the envelope's JobID is
// always set for job-scoped topics by the producers in this project.
func jobIDFor(env events.Envelope) string { return env.JobID }
