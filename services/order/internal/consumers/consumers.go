// Package consumers applies incoming Kafka events to the Order aggregate:
// restaurant decisions, payment outcomes and delivery progress. Every
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
	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/store"
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
	case events.RestaurantAccepted:
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			o.RestaurantAccepted = true
			return "", nil
		})
	case events.RestaurantRejected:
		data, err := events.Decode[events.RestaurantDecisionData](env)
		if err != nil {
			return err
		}
		return h.cancel(ctx, env, data.Reason)
	case events.RestaurantReady:
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			return o.Transition(events.StatusReady, "")
		})
	case events.PaymentCompleted:
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			o.Paid = true
			return "", nil
		})
	case events.PaymentFailed:
		data, err := events.Decode[events.PaymentData](env)
		if err != nil {
			return err
		}
		return h.cancel(ctx, env, data.Reason)
	case events.DeliveryAssigned:
		data, err := events.Decode[events.DeliveryAssignedData](env)
		if err != nil {
			return err
		}
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			o.DriverID = data.DriverID
			return "", nil
		})
	case events.DeliveryPickedUp:
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			return o.Transition(events.StatusOutForDelivery, "")
		})
	case events.DeliveryCompleted:
		return h.withOrder(ctx, env, func(o *domain.Order) (string, error) {
			return o.Transition(events.StatusDelivered, "")
		})
	default:
		h.Log.Debug("ignoring unhandled topic", "topic", env.EventType)
		return nil
	}
}

// withOrder loads the order for update, applies mutate, persists any status
// change plus flag changes, tries MaybeConfirm, and inserts the resulting
// outbox events — all inside one transaction guarded by the idempotency
// check. mutate may change flags without transitioning (return "" as the
// "from" status) or perform its own explicit Transition.
func (h *Handlers) withOrder(ctx context.Context, env events.Envelope, mutate func(o *domain.Order) (from string, err error)) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			h.Log.Info("duplicate event skipped", "eventId", env.EventID, "topic", env.EventType)
			return nil
		}

		orderID := orderIDFor(env)
		o, err := store.GetForUpdate(ctx, tx, orderID)
		if err != nil {
			return fmt.Errorf("load order %s: %w", orderID, err)
		}

		explicitFrom, err := mutate(o)
		if err != nil {
			return fmt.Errorf("apply event %s to order %s: %w", env.EventType, orderID, err)
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
			return fmt.Errorf("auto-confirm order %s: %w", orderID, err)
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

// cancel transitions the order to CANCELLED and emits both
// orders.status-changed and orders.cancelled — the latter carries the line
// items so the restaurant can release stock and payment can issue a refund.
func (h *Handlers) cancel(ctx context.Context, env events.Envelope, reason string) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		orderID := orderIDFor(env)
		o, err := store.GetForUpdate(ctx, tx, orderID)
		if err != nil {
			return fmt.Errorf("load order %s: %w", orderID, err)
		}
		from, err := o.Transition(events.StatusCancelled, reason)
		if err != nil {
			// Already terminal (e.g. two failure events racing) — safe to ignore.
			h.Log.Warn("cancel ignored", "orderId", orderID, "status", o.Status, "error", err)
			return nil
		}
		if err := store.ApplyTransition(ctx, tx, o, from); err != nil {
			return err
		}
		if err := insertStatusChanged(ctx, tx, o, from); err != nil {
			return err
		}

		var items []events.OrderItem
		for _, it := range o.Items {
			items = append(items, events.OrderItem{MenuItemID: it.MenuItemID, Name: it.Name, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
		}
		cancelledEnv, err := events.New(events.OrderCancelled, o.ID, events.OrderCancelledData{
			OrderID: o.ID, CustomerID: o.CustomerID, RestaurantID: o.RestaurantID, Items: items, Reason: reason,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, cancelledEnv)
	})
}

func insertStatusChanged(ctx context.Context, tx pgx.Tx, o *domain.Order, from string) error {
	env, err := events.New(events.OrderStatusChanged, o.ID, events.OrderStatusChangedData{
		OrderID: o.ID, CustomerID: o.CustomerID, RestaurantID: o.RestaurantID, DriverID: o.DriverID,
		From: from, To: o.Status, Reason: o.CancelReason,
	})
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}

// orderIDFor reads the orderId every consumed event carries either at the
// envelope level or inside its data payload; the envelope's OrderID is
// always set for order-scoped topics by the producers in this project.
func orderIDFor(env events.Envelope) string { return env.OrderID }
