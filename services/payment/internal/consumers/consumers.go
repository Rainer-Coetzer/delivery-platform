// Package consumers charges an order when it is created and refunds it when
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
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/store"
)

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.OrderCreated:
		data, err := events.Decode[events.OrderCreatedData](env)
		if err != nil {
			return err
		}
		return h.charge(ctx, env, data)
	case events.OrderCancelled:
		data, err := events.Decode[events.OrderCancelledData](env)
		if err != nil {
			return err
		}
		return h.refund(ctx, env, data)
	default:
		return nil
	}
}

func (h *Handlers) charge(ctx context.Context, env events.Envelope, data events.OrderCreatedData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		approved, reason := domain.Authorize(data.PaymentMethod, data.TotalCents)
		status := "COMPLETED"
		if !approved {
			status = "FAILED"
		}
		p := &store.Payment{
			ID: uuid.NewString(), OrderID: data.OrderID, IdempotencyKey: data.OrderID,
			CustomerID: data.CustomerID, AmountCents: data.TotalCents, Method: data.PaymentMethod,
			Status: status, FailureReason: reason,
		}
		existed, err := store.InsertIfAbsent(ctx, tx, p)
		if err != nil {
			return err
		}
		if existed {
			h.Log.Info("order already charged, skipping duplicate authorization", "orderId", data.OrderID)
			return nil
		}

		topic := events.PaymentCompleted
		if !approved {
			topic = events.PaymentFailed
		}
		out, err := events.New(topic, data.OrderID, events.PaymentData{
			OrderID: data.OrderID, PaymentID: p.ID, AmountCents: data.TotalCents, Reason: reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, out)
	})
}

func (h *Handlers) refund(ctx context.Context, env events.Envelope, data events.OrderCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		p, err := store.GetByOrderForUpdate(ctx, tx, data.OrderID)
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
		if err := store.InsertRefund(ctx, tx, uuid.NewString(), p.ID, data.OrderID, p.AmountCents); err != nil {
			return err
		}
		out, err := events.New(events.PaymentRefunded, data.OrderID, events.PaymentData{
			OrderID: data.OrderID, PaymentID: p.ID, AmountCents: p.AmountCents, Reason: data.Reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, out)
	})
}
