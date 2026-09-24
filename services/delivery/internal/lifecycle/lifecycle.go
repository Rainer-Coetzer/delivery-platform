// Package lifecycle moves a delivery through ASSIGNED -> PICKED_UP ->
// COMPLETED. It is the one place that does this, so the driver's app (via
// internal/api) and the demo autopilot (via internal/sim) can't drift apart
// on what a transition writes and publishes.
package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

const (
	StatusAssigned  = "ASSIGNED"
	StatusPickedUp  = "PICKED_UP"
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"
)

// ErrWrongState means the delivery isn't in the status the caller expected,
// e.g. a driver tapping "delivered" twice, or the autopilot racing a real
// driver. Callers treat it as a conflict, not a failure.
var ErrWrongState = errors.New("delivery not in expected state")

// topicFor maps the target status to the event announcing it.
var topicFor = map[string]string{
	StatusPickedUp:  events.DeliveryPickedUp,
	StatusCompleted: events.DeliveryCompleted,
}

// Advance moves delivery id from `from` to `to` in one transaction: status
// and timestamp, the driver back to AVAILABLE on completion, and the outbox
// event. Returns store.ErrNotFound or ErrWrongState for the caller to map.
func Advance(ctx context.Context, pool *db.Pool, id, from, to string) error {
	topic, ok := topicFor[to]
	if !ok {
		return fmt.Errorf("lifecycle: no event defined for target status %q", to)
	}
	return pool.WithTx(ctx, func(tx pgx.Tx) error {
		d, err := store.GetForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Status != from {
			return fmt.Errorf("%w: delivery %s is %s, expected %s", ErrWrongState, id, d.Status, from)
		}
		elapsed, err := store.AdvanceStatus(ctx, tx, id, to)
		if err != nil {
			return err
		}

		data := events.DeliveryProgressData{OrderID: d.OrderID, DeliveryID: d.ID, DriverID: d.DriverID}
		if to == StatusCompleted {
			if err := store.MarkDriverAvailable(ctx, tx, d.DriverID); err != nil {
				return err
			}
			// Assignment to hand-over, which is what admin reports as
			// delivery time. Was never set before (audit P1, dead field).
			data.DurationSeconds = elapsed
		}

		env, err := events.New(topic, d.OrderID, data)
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
}
