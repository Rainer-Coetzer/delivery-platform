// Package progress moves an assignment through ASSIGNED -> PICKED_UP ->
// COMPLETED. It is the one place that does this, so the agent's app (via
// internal/api) and the demo autopilot (via internal/sim) can't drift apart
// on what a transition writes and publishes.
package progress

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

const (
	StatusAssigned  = "ASSIGNED"
	StatusPickedUp  = "PICKED_UP"
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"
)

// ErrWrongState means the assignment isn't in the status the caller expected,
// e.g. an agent tapping "delivered" twice, or the autopilot racing a real
// agent. Callers treat it as a conflict, not a failure.
var ErrWrongState = errors.New("assignment not in expected state")

// Advance moves assignment id from `from` to `to` in one transaction: status
// and timestamp, the agent back to AVAILABLE on completion, and a
// dispatch.stop-completed for the stop just finished (the pickup for
// PICKED_UP, the dropoff for COMPLETED). Returns store.ErrNotFound or
// ErrWrongState for the caller to map.
func Advance(ctx context.Context, pool *db.Pool, id, from, to string) error {
	if to != StatusPickedUp && to != StatusCompleted {
		return fmt.Errorf("progress: %q is not a stop being completed", to)
	}
	return pool.WithTx(ctx, func(tx pgx.Tx) error {
		d, err := store.GetForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Status != from {
			return fmt.Errorf("%w: assignment %s is %s, expected %s", ErrWrongState, id, d.Status, from)
		}
		elapsed, err := store.AdvanceStatus(ctx, tx, id, to)
		if err != nil {
			return err
		}

		data := events.StopCompletedData{JobID: d.JobID, AssignmentID: d.ID, AgentID: d.AgentID, StopSeq: d.PickupSeq}
		if to == StatusCompleted {
			data.StopSeq = d.DropoffSeq
			if err := store.MarkAgentAvailable(ctx, tx, d.AgentID); err != nil {
				return err
			}
			// Assignment to hand-over, which is what admin reports as
			// assignment time. Was never set before (audit P1, dead field).
			data.DurationSeconds = elapsed
		}

		env, err := events.New(events.DispatchStopCompleted, d.JobID, data)
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
}
