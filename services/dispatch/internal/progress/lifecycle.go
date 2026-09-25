// Package progress moves an assignment along its route, one stop at a
// time: ASSIGNED, then PICKED_UP once the first stop is done (and while
// the rest are underway), then COMPLETED with the last. It is the one
// place that does this, so the agent's app (via internal/api) and the demo
// autopilot (via internal/sim) can't drift apart on what completing a stop
// writes and publishes.
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
	StatusPickedUp  = "PICKED_UP" // the first stop is done; the rest may still be underway
	StatusCompleted = "COMPLETED"
	StatusCancelled = "CANCELLED"
)

// ErrProofMissing: the stop needs proof (the pack's job.proof) and none
// has been captured yet.
var ErrProofMissing = errors.New("stop needs proof first")

// ErrWrongState means the assignment can't take this step: it's finished,
// or the stop isn't the next one (an agent tapping "delivered" twice, the
// autopilot racing a real agent, a stop skipped). Callers treat it as a
// conflict, not a failure.
var ErrWrongState = errors.New("assignment not in expected state")

// CompleteStop marks stop seq done in one transaction: the stop, the
// assignment's status and timestamps, the agent back to AVAILABLE after
// the last stop, and a dispatch.stop-completed for the jobs module. Stops
// go in order: only the next open stop can be completed. Returns
// store.ErrNotFound or ErrWrongState for the caller to map.
func CompleteStop(ctx context.Context, pool *db.Pool, id string, seq int) error {
	return pool.WithTx(ctx, func(tx pgx.Tx) error {
		d, err := store.GetForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Status != StatusAssigned && d.Status != StatusPickedUp {
			return fmt.Errorf("%w: assignment %s is %s", ErrWrongState, id, d.Status)
		}
		if d.Next == nil {
			return fmt.Errorf("assignment %s is %s but has no open stop: its route is missing", id, d.Status)
		}
		if d.Next.Seq != seq {
			return fmt.Errorf("%w: stop %d is next on assignment %s, not %d", ErrWrongState, d.Next.Seq, id, seq)
		}
		stops, err := store.Stops(ctx, tx, id)
		if err != nil {
			return err
		}
		last := seq == stops[len(stops)-1].Seq
		var ref *events.ProofRef
		if need := d.Next.Proof; need != "" {
			p, err := store.GetProof(ctx, tx, id, seq)
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("%w: stop %d of assignment %s needs a %s", ErrProofMissing, seq, id, need)
			}
			if err != nil {
				return err
			}
			ref = &events.ProofRef{Kind: p.Kind, AssignmentID: id}
		}

		if err := store.MarkStopDone(ctx, tx, id, seq); err != nil {
			return err
		}
		to := StatusPickedUp
		if last {
			to = StatusCompleted
		}
		elapsed, err := store.AdvanceStatus(ctx, tx, id, to)
		if err != nil {
			return err
		}

		data := events.StopCompletedData{JobID: d.JobID, AssignmentID: d.ID, AgentID: d.AgentID, StopSeq: seq, Proof: ref}
		if last {
			if err := store.MarkAgentAvailable(ctx, tx, d.AgentID); err != nil {
				return err
			}
			// Assignment to hand-over, which is what admin reports as
			// assignment time.
			data.DurationSeconds = elapsed
		}
		env, err := events.New(events.DispatchStopCompleted, d.JobID, data)
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
}
