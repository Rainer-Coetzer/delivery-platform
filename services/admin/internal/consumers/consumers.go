// Package consumers keeps the admin read model (orders_projection) current
// from order, restaurant-adjacent and delivery events.
package consumers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/store"
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
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			return store.UpsertCreated(ctx, tx, data.OrderID, data.RestaurantID, data.RestaurantName, data.CustomerID, data.TotalCents, env.Timestamp)
		})
	case events.OrderStatusChanged:
		data, err := events.Decode[events.OrderStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			var deliveredAt *time.Time
			if data.To == events.StatusDelivered {
				t := env.Timestamp
				deliveredAt = &t
			}
			return store.UpdateStatus(ctx, tx, data.OrderID, data.To, deliveredAt)
		})
	case events.OrderCancelled:
		data, err := events.Decode[events.OrderCancelledData](env)
		if err != nil {
			return err
		}
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			return store.UpdateStatus(ctx, tx, data.OrderID, events.StatusCancelled, nil)
		})
	case events.DeliveryAssigned:
		data, err := events.Decode[events.DeliveryAssignedData](env)
		if err != nil {
			return err
		}
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			return store.SetDriver(ctx, tx, data.OrderID, data.DriverID)
		})
	default:
		return nil
	}
}

func (h *Handlers) withDedup(ctx context.Context, env events.Envelope, apply func(tx pgx.Tx) error) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		return apply(tx)
	})
}
