// Package consumers assigns a driver as soon as a restaurant marks an order
// ready (or queues the order for retry if no driver is free), and releases
// the driver again if the order is cancelled.
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
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

// Topics this service consumes, kept next to the handlers that use them.
var Topics = []string{events.RestaurantReady, events.OrderCancelled}

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.RestaurantReady:
		data, err := events.Decode[events.RestaurantReadyData](env)
		if err != nil {
			return err
		}
		return h.handleReady(ctx, env, data)
	case events.OrderCancelled:
		data, err := events.Decode[events.OrderCancelledData](env)
		if err != nil {
			return err
		}
		return h.handleCancelled(ctx, env, data)
	default:
		return nil
	}
}

func (h *Handlers) handleReady(ctx context.Context, env events.Envelope, data events.RestaurantReadyData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		outcome, err := TryAssign(ctx, tx, data.OrderID, data.RestaurantID, data.Pickup, data.Dropoff)
		if err != nil {
			return err
		}
		switch outcome {
		case OrderCancelled:
			h.Log.Info("order-ready for a cancelled order, not dispatching", "orderId", data.OrderID)
		case NoDriverFree:
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

// handleCancelled tombstones the order, drops any queued assignment, and if
// a driver was already on it, cancels the delivery and puts the driver back
// in the pool (audit P0-10: before this, cancelled orders kept drivers BUSY
// forever and pending rows retried forever).
func (h *Handlers) handleCancelled(ctx context.Context, env events.Envelope, data events.OrderCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		if err := store.LockOrder(ctx, tx, data.OrderID); err != nil {
			return err
		}
		if err := store.MarkOrderCancelled(ctx, tx, data.OrderID); err != nil {
			return err
		}
		if err := store.DeletePending(ctx, tx, data.OrderID); err != nil {
			return err
		}

		d, err := store.GetByOrderForUpdate(ctx, tx, data.OrderID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // never dispatched: the tombstone and pending delete are all there is to do
		}
		if err != nil {
			return err
		}
		if d.Status != lifecycle.StatusAssigned && d.Status != lifecycle.StatusPickedUp {
			return nil // already completed or cancelled
		}
		if err := store.UpdateStatus(ctx, tx, d.ID, lifecycle.StatusCancelled); err != nil {
			return err
		}
		h.Log.Info("delivery cancelled, driver released", "orderId", data.OrderID, "driverId", d.DriverID)
		return store.MarkDriverAvailable(ctx, tx, d.DriverID)
	})
}

// AssignOutcome says what TryAssign did, so callers can queue, skip or
// clean up without guessing from a bool.
type AssignOutcome int

const (
	Assigned AssignOutcome = iota
	NoDriverFree
	OrderCancelled
)

// TryAssign locks and claims the nearest AVAILABLE driver to the pickup
// point, creates the delivery, and inserts the delivery.assigned outbox
// event. It never assigns a tombstoned (cancelled) order.
func TryAssign(ctx context.Context, tx pgx.Tx, orderID, restaurantID string, pickup, dropoff events.Location) (AssignOutcome, error) {
	if err := store.LockOrder(ctx, tx, orderID); err != nil {
		return 0, err
	}
	cancelled, err := store.IsOrderCancelled(ctx, tx, orderID)
	if err != nil {
		return 0, err
	}
	if cancelled {
		return OrderCancelled, nil
	}

	driver, err := store.FindNearestAvailableForUpdate(ctx, tx, pickup.Lat, pickup.Lng)
	if errors.Is(err, store.ErrNotFound) {
		return NoDriverFree, nil
	}
	if err != nil {
		return 0, err
	}
	if err := store.MarkDriverBusy(ctx, tx, driver.ID); err != nil {
		return 0, err
	}

	eta := domain.ETAMinutes(domain.HaversineMeters(driver.Lat, driver.Lng, pickup.Lat, pickup.Lng))
	delivery := &store.Delivery{
		ID: uuid.NewString(), OrderID: orderID, DriverID: driver.ID, RestaurantID: restaurantID,
		PickupLat: pickup.Lat, PickupLng: pickup.Lng, DropoffLat: dropoff.Lat, DropoffLng: dropoff.Lng,
		Status: lifecycle.StatusAssigned, EtaMinutes: eta,
	}
	if err := store.InsertDelivery(ctx, tx, delivery); err != nil {
		return 0, err
	}

	env, err := events.New(events.DeliveryAssigned, orderID, events.DeliveryAssignedData{
		OrderID: orderID, DeliveryID: delivery.ID, DriverID: driver.ID, DriverName: driver.Name,
		EtaMinutes: eta, DriverAt: events.Location{Lat: driver.Lat, Lng: driver.Lng},
	})
	if err != nil {
		return 0, err
	}
	if err := outbox.Insert(ctx, tx, env); err != nil {
		return 0, err
	}
	return Assigned, nil
}
