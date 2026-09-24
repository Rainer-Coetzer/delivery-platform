// Package consumers reacts to orders.created (accept or reject based on
// opening hours and stock) and orders.cancelled (release any reserved
// stock).
package consumers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
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
		return h.handleOrderCreated(ctx, env, data)
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
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		if err := store.InsertRestaurantOrder(ctx, tx, &store.RestaurantOrder{
			OrderID: data.OrderID, RestaurantID: data.RestaurantID, Status: "PENDING",
			Items: data.Items, Pickup: data.Pickup, Dropoff: data.Dropoff,
		}); err != nil {
			return err
		}

		rest, err := loadRestaurant(ctx, tx, data.RestaurantID)
		if err != nil {
			return err
		}

		var requested []domain.RequestedItem
		var ids []string
		for _, it := range data.Items {
			requested = append(requested, domain.RequestedItem{MenuItemID: it.MenuItemID, Quantity: it.Quantity})
			ids = append(ids, it.MenuItemID)
		}

		reason := ""
		ok := rest.OpenAt(minutesSinceMidnight(env.Timestamp))
		if !ok {
			reason = rest.Name + " is currently closed"
		} else {
			catalogue, err := store.CatalogueFor(ctx, tx, ids)
			if err != nil {
				return err
			}
			ok, reason = domain.CheckStock(catalogue, requested)
		}

		if !ok {
			if err := store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, "REJECTED", reason); err != nil {
				return err
			}
			return publishDecision(ctx, tx, events.RestaurantRejected, data.OrderID, data.RestaurantID, reason)
		}

		if err := store.DecrementStock(ctx, tx, requested); err != nil {
			return err
		}
		if err := store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, "ACCEPTED", ""); err != nil {
			return err
		}
		return publishDecision(ctx, tx, events.RestaurantAccepted, data.OrderID, data.RestaurantID, "")
	})
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
		if err != nil {
			return err
		}
		// Only release stock if it was actually reserved (order reached
		// ACCEPTED or READY); a rejected order never touched stock.
		if ro.Status == "ACCEPTED" || ro.Status == "READY" {
			var requested []domain.RequestedItem
			for _, it := range ro.Items {
				requested = append(requested, domain.RequestedItem{MenuItemID: it.MenuItemID, Quantity: it.Quantity})
			}
			if err := store.RestoreStock(ctx, tx, requested); err != nil {
				return err
			}
		}
		return store.UpdateRestaurantOrderStatus(ctx, tx, data.OrderID, "CANCELLED", "")
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

func minutesSinceMidnight(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func loadRestaurant(ctx context.Context, tx pgx.Tx, id string) (*domain.Restaurant, error) {
	row := tx.QueryRow(ctx,
		`SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, is_open FROM restaurants WHERE id=$1`,
		id)
	r := &domain.Restaurant{}
	var opensMin, closesMin float64
	if err := row.Scan(&r.ID, &r.Name, &r.Lat, &r.Lng, &opensMin, &closesMin, &r.IsOpen); err != nil {
		return nil, err
	}
	r.OpensAt = time.Duration(opensMin) * time.Minute
	r.ClosesAt = time.Duration(closesMin) * time.Minute
	return r, nil
}
