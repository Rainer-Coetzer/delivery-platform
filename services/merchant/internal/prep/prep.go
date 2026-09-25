// Package prep marks a job READY and publishes merchant.job-ready.
// It is shared by staff pressing the button (internal/api) and the demo
// autopilot (Autopilot below), so both write and publish the same thing.
package prep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/store"
)

// ErrNotPreparing: only a job the jobs module has confirmed (paid and
// accepted, so now PREPARING) can be marked ready. Allowing it from
// ACCEPTED let READY race ahead of payment, and the jobs module then
// dead-lettered it while delivery still dispatched an agent (audit P1).
var ErrNotPreparing = errors.New("job is not being prepared")

// MarkReady marks jobID ready. merchantID, when non-empty, must own the
// job; a mismatch reports store.ErrNotFound so one merchant can't probe
// another's job ids. The autopilot passes "" (it acts for every prep).
func MarkReady(ctx context.Context, pool *db.Pool, merchantID, jobID string) error {
	return pool.WithTx(ctx, func(tx pgx.Tx) error {
		ro, err := store.GetMerchantJobForUpdate(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if merchantID != "" && ro.MerchantID != merchantID {
			return store.ErrNotFound
		}
		if ro.Status != store.StatusPreparing {
			return fmt.Errorf("%w: job %s is %s", ErrNotPreparing, jobID, ro.Status)
		}
		if err := store.UpdateMerchantJobStatus(ctx, tx, jobID, store.StatusReady, ""); err != nil {
			return err
		}
		env, err := events.New(events.MerchantReady, jobID, events.MerchantReadyData{
			JobID: jobID, MerchantID: ro.MerchantID, Pickup: ro.Pickup, Dropoff: ro.Dropoff,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
}

// Autopilot (DEMO_AUTOPILOT) marks PREPARING jobs ready once PrepTime has
// passed, standing in for prep staff so a demo job completes on its
// own. Off by default.
type Autopilot struct {
	Pool     *db.Pool
	Store    *store.Store
	Log      *slog.Logger
	PrepTime time.Duration
	Interval time.Duration
}

func (a *Autopilot) Run(ctx context.Context) {
	if a.Interval == 0 {
		a.Interval = 2 * time.Second
	}
	ticker := time.NewTicker(a.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Autopilot) tick(ctx context.Context) {
	preparing, err := a.Store.ListPreparing(ctx)
	if err != nil {
		a.Log.Error("autopilot: list preparing jobs", "error", err)
		return
	}
	now := time.Now()
	for _, p := range preparing {
		if !domain.DueForReady(now, p.PreparingSince, a.PrepTime) {
			continue
		}
		err := MarkReady(ctx, a.Pool, "", p.JobID)
		switch {
		case err == nil:
			a.Log.Info("autopilot marked job ready", "jobId", p.JobID)
		case errors.Is(err, ErrNotPreparing):
			// Staff pressed the button, or it was cancelled, between list and lock.
		default:
			a.Log.Error("autopilot: mark ready", "jobId", p.JobID, "error", err)
		}
	}
}
