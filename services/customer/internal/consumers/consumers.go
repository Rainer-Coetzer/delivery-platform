// Package consumers projects orders.created and orders.status-changed into
// this service's order_history read model.
package consumers

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/customer/internal/store"
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
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			return store.UpsertOrderCreated(ctx, tx, &store.OrderHistoryEntry{
				OrderID: data.OrderID, CustomerID: data.CustomerID, RestaurantName: data.RestaurantName,
				Status: "CREATED", TotalCents: data.TotalCents,
			}, env.Timestamp)
		})
	case events.OrderStatusChanged:
		data, err := events.Decode[events.OrderStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			return store.UpdateOrderStatus(ctx, tx, data.OrderID, data.To)
		})
	case events.OrderCancelled:
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			data, err := events.Decode[events.OrderCancelledData](env)
			if err != nil {
				return err
			}
			return store.UpdateOrderStatus(ctx, tx, data.OrderID, events.StatusCancelled)
		})
	default:
		return nil
	}
}
