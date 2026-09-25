// Package consumers charges a job when it is created and refunds it when
// it is cancelled after a successful charge.
package consumers

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/payment"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/store"
)

type Handlers struct {
	Pool     *db.Pool
	Log      *slog.Logger
	Payments *payment.Set
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.JobCreated:
		data, err := events.Decode[events.JobCreatedData](env)
		if err != nil {
			return err
		}
		return h.charge(ctx, env, data)
	case events.JobCancelled:
		data, err := events.Decode[events.JobCancelledData](env)
		if err != nil {
			return err
		}
		return h.refund(ctx, env, data)
	default:
		return nil
	}
}

func (h *Handlers) charge(ctx context.Context, env events.Envelope, data events.JobCreatedData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		res, err := h.authorize(ctx, data)
		if err != nil {
			return err // couldn't ask the gateway: retry the event
		}
		approved, reason := res.Approved, res.Reason
		status := "COMPLETED"
		if !approved {
			status = "FAILED"
		}
		p := &store.Payment{
			ID: uuid.NewString(), JobID: data.JobID, IdempotencyKey: data.JobID,
			CustomerID: data.CustomerID, AmountCents: data.TotalCents, Method: data.PaymentMethod,
			Status: status, FailureReason: reason,
		}
		existed, err := store.InsertIfAbsent(ctx, tx, p)
		if err != nil {
			return err
		}
		if existed {
			h.Log.Info("job already charged, skipping duplicate authorization", "jobId", data.JobID)
			return nil
		}

		topic := events.PaymentCompleted
		if !approved {
			topic = events.PaymentFailed
		}
		out, err := events.New(topic, data.JobID, events.PaymentData{
			JobID: data.JobID, PaymentID: p.ID, AmountCents: data.TotalCents, Reason: reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, out)
	})
}

// authorize asks the pack's provider for the job's payment method. The jobs
// API only accepts methods the pack has, so a missing one means the pack
// changed under a job in flight: decline it, which cancels the job with a
// reason a person can read, rather than retrying forever.
func (h *Handlers) authorize(ctx context.Context, data events.JobCreatedData) (payment.Result, error) {
	p, ok := h.Payments.For(data.PaymentMethod)
	if !ok {
		return payment.Result{Reason: "payment method " + data.PaymentMethod + " is not accepted"}, nil
	}
	return p.Authorize(ctx, payment.Charge{JobID: data.JobID, AmountCents: data.TotalCents, Method: data.PaymentMethod})
}

func (h *Handlers) refund(ctx context.Context, env events.Envelope, data events.JobCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		p, err := store.GetByJobForUpdate(ctx, tx, data.JobID)
		if err != nil {
			if err == store.ErrNotFound {
				return nil // never charged (e.g. rejected before payment ran) — nothing to refund
			}
			return err
		}
		if p.Status != "COMPLETED" {
			return nil // already failed or already refunded
		}

		if err := store.MarkRefunded(ctx, tx, p.ID); err != nil {
			return err
		}
		if err := store.InsertRefund(ctx, tx, uuid.NewString(), p.ID, data.JobID, p.AmountCents); err != nil {
			return err
		}
		out, err := events.New(events.PaymentRefunded, data.JobID, events.PaymentData{
			JobID: data.JobID, PaymentID: p.ID, AmountCents: p.AmountCents, Reason: data.Reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, out)
	})
}
