// Package consumers assigns a driver as soon as a restaurant marks an order
// ready, or queues the order for retry if no driver is free.
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
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	if env.EventType != events.RestaurantReady {
		return nil
	}
	data, err := events.Decode[events.RestaurantReadyData](env)
	if err != nil {
		return err
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}
		assigned, err := TryAssign(ctx, tx, data.OrderID, data.RestaurantID, data.Pickup, data.Dropoff)
		if err != nil {
			return err
		}
		if !assigned {
			h.Log.Info("no driver available, queued for retry", "orderId", data.OrderID)
			return store.InsertPending(ctx, tx, &store.PendingAssignment{
				OrderID: data.OrderID, RestaurantID: data.RestaurantID,
				PickupLat: data.Pickup.Lat, PickupLng: data.Pickup.Lng,
				DropoffLat: data.Dropoff.Lat, DropoffLng: data.Dropoff.Lng,
			})
		}
		return nil
	})
}

// TryAssign locks and claims the nearest AVAILABLE driver to the pickup
// point, creates the delivery, and inserts the delivery.assigned outbox
// event. It reports false (with no error) when no driver is currently free,
// so the caller can queue a retry instead of failing the whole transaction.
func TryAssign(ctx context.Context, tx pgx.Tx, orderID, restaurantID string, pickup, dropoff events.Location) (bool, error) {
	driver, err := store.FindNearestAvailableForUpdate(ctx, tx, pickup.Lat, pickup.Lng)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := store.MarkDriverBusy(ctx, tx, driver.ID); err != nil {
		return false, err
	}

	distance := domain.HaversineMeters(driver.Lat, driver.Lng, pickup.Lat, pickup.Lng)
	eta := domain.ETAMinutes(distance)

	delivery := &store.Delivery{
		ID: uuid.NewString(), OrderID: orderID, DriverID: driver.ID, RestaurantID: restaurantID,
		PickupLat: pickup.Lat, PickupLng: pickup.Lng, DropoffLat: dropoff.Lat, DropoffLng: dropoff.Lng,
		Status: "ASSIGNED", EtaMinutes: eta,
	}
	if err := store.InsertDelivery(ctx, tx, delivery); err != nil {
		return false, err
	}

	env, err := events.New(events.DeliveryAssigned, orderID, events.DeliveryAssignedData{
		OrderID: orderID, DeliveryID: delivery.ID, DriverID: driver.ID, DriverName: driver.Name,
		EtaMinutes: eta, DriverAt: events.Location{Lat: driver.Lat, Lng: driver.Lng},
	})
	if err != nil {
		return false, err
	}
	if err := outbox.Insert(ctx, tx, env); err != nil {
		return false, err
	}
	return true, nil
}
