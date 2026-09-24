// Package consumers reacts to orders.created (accept or reject based on
// opening hours and stock), orders.status-changed (the kitchen starts once
// the order reaches PREPARING) and orders.cancelled (release any reserved
// stock).
package consumers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
)

// Topics this service consumes, kept next to the handlers that use them.
var Topics = []string{events.OrderCreated, events.OrderStatusChanged, events.OrderCancelled}

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
		return h.handleOrderCreated(ctx, env, data)
	case events.OrderStatusChanged:
		data, err := events.Decode[events.OrderStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.handleStatusChanged(ctx, env, data)
	case events.OrderCancelled:
		data, err := events.Decode[events.OrderCancelledData](env)
		if err != nil {
			return err
		}
		return h.handleOrderCancelled(ctx, env, data)
	default:
		return nil
	}
}

func (h *Handlers) handleOrderCreated(ctx context.Context, env events.Envelope, data events.OrderCreatedData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}

		// An unknown restaurant gets an explicit rejection. It used to fail
		// the lookup, retry, dead-letter, and leave the order CREATED
		// forever. No kitchen row is cached for it (restaurant_id is a
		// foreign key), and the cancel handler tolerates that.
		rest, err := store.GetRestaurantTx(ctx, tx, data.RestaurantID)
		if errors.Is(err, store.ErrNotFound) {
			return publishDecision(ctx, tx, events.RestaurantRejected, data.OrderID, data.RestaurantID, "restaurant not found")
		}
		if err != nil {
			return err
		}

		if err := store.InsertRestaurantOrder(ctx, tx, &store.RestaurantOrder{
			OrderID: data.OrderID, RestaurantID: data.RestaurantID, Status: store.StatusPending,
			Items: data.Items, Pickup: data.Pickup, Dropoff: data.Dropoff,
		}); err != nil {
			return err
		}

		reason, err := h.decide(ctx, tx, rest, env, data)
		if err != nil {
			return err
		}
		if reason != "" {
			if err := store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, store.StatusRejected, reason); err != nil {
				return err
			}
			return publishDecision(ctx, tx, events.RestaurantRejected, data.OrderID, data.RestaurantID, reason)
		}

		if err := store.DecrementStock(ctx, tx, requestedFrom(data.Items)); err != nil {
			return err
		}
		if err := store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, store.StatusAccepted, ""); err != nil {
			return err
		}
		return publishDecision(ctx, tx, events.RestaurantAccepted, data.OrderID, data.RestaurantID, "")
	})
}

// decide returns a rejection reason, or "" to accept. Hours are judged in
// the restaurant's own timezone at the moment the order was placed.
func (h *Handlers) decide(ctx context.Context, tx pgx.Tx, rest *domain.Restaurant, env events.Envelope, data events.OrderCreatedData) (string, error) {
	open, err := rest.OpenAtTime(env.Timestamp)
	if err != nil {
		// Bad data on our side, not the customer's: reject rather than
		// retry forever, and log it loudly so someone fixes the row.
		h.Log.Error("restaurant timezone misconfigured", "restaurantId", rest.ID, "error", err)
		return rest.Name + " can't take orders right now", nil
	}
	if !open {
		return rest.Name + " is currently closed", nil
	}

	requested := requestedFrom(data.Items)
	ids := make([]string, 0, len(requested))
	for _, r := range requested {
		ids = append(ids, r.MenuItemID)
	}
	catalogue, err := store.CatalogueFor(ctx, tx, ids)
	if err != nil {
		return "", err
	}
	if ok, reason := domain.CheckItems(rest.ID, catalogue, requested); !ok {
		return reason, nil
	}
	return "", nil
}

func requestedFrom(items []events.OrderItem) []domain.RequestedItem {
	out := make([]domain.RequestedItem, 0, len(items))
	for _, it := range items {
		out = append(out, domain.RequestedItem{MenuItemID: it.MenuItemID, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
	}
	return out
}

func (h *Handlers) handleOrderCancelled(ctx context.Context, env events.Envelope, data events.OrderCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}
		ro, err := store.GetRestaurantOrderForUpdate(ctx, tx, data.OrderID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // rejected as "restaurant not found": nothing was cached or reserved
		}
		if err != nil {
			return err
		}
		// Only release stock if it was actually reserved (accepted and
		// beyond); a rejected order never touched stock.
		if ro.Status == store.StatusAccepted || ro.Status == store.StatusPreparing || ro.Status == store.StatusReady {
			if err := store.RestoreStock(ctx, tx, requestedFrom(ro.Items)); err != nil {
				return err
			}
		}
		return store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, store.StatusCancelled, "")
	})
}

// handleStatusChanged tracks the Order service's CONFIRMED -> PREPARING
// step: that is the moment payment and acceptance are both in, so the
// kitchen may start and the order may later be marked ready.
func (h *Handlers) handleStatusChanged(ctx context.Context, env events.Envelope, data events.OrderStatusChangedData) error {
	if data.To != events.StatusPreparing {
		return nil
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		moved, err := store.MarkPreparing(ctx, tx, data.OrderID, env.Timestamp)
		if err != nil {
			return err
		}
		if !moved {
			h.Log.Warn("PREPARING for an order not in ACCEPTED, ignoring", "orderId", data.OrderID)
		}
		return nil
	})
}

func publishDecision(ctx context.Context, tx pgx.Tx, topic, orderID, restaurantID, reason string) error {
	env, err := events.New(topic, orderID, events.RestaurantDecisionData{
		OrderID: orderID, RestaurantID: restaurantID, Reason: reason,
	})
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}
