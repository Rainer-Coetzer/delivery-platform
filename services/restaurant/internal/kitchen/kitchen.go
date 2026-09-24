// Package kitchen marks an order READY and publishes restaurant.order-ready.
// It is shared by staff pressing the button (internal/api) and the demo
// autopilot (Autopilot below), so both write and publish the same thing.
package kitchen

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
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
)

// ErrNotPreparing: only an order the Order service has confirmed (paid and
// accepted, so now PREPARING) can be marked ready. Allowing it from
// ACCEPTED let READY race ahead of payment, and the Order service then
// dead-lettered it while delivery still dispatched a driver (audit P1).
var ErrNotPreparing = errors.New("order is not being prepared")

// MarkReady marks orderID ready. restaurantID, when non-empty, must own the
// order; a mismatch reports store.ErrNotFound so one restaurant can't probe
// another's order ids. The autopilot passes "" (it acts for every kitchen).
func MarkReady(ctx context.Context, pool *db.Pool, restaurantID, orderID string) error {
	return pool.WithTx(ctx, func(tx pgx.Tx) error {
		ro, err := store.GetRestaurantOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if restaurantID != "" && ro.RestaurantID != restaurantID {
			return store.ErrNotFound
		}
		if ro.Status != store.StatusPreparing {
			return fmt.Errorf("%w: order %s is %s", ErrNotPreparing, orderID, ro.Status)
		}
		if err := store.UpdateRestaurantOrderStatus(ctx, tx, orderID, store.StatusReady, ""); err != nil {
			return err
		}
		env, err := events.New(events.RestaurantReady, orderID, events.RestaurantReadyData{
			OrderID: orderID, RestaurantID: ro.RestaurantID, Pickup: ro.Pickup, Dropoff: ro.Dropoff,
		})
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, env)
	})
}

// Autopilot (DEMO_AUTOPILOT) marks PREPARING orders ready once PrepTime has
// passed, standing in for kitchen staff so a demo order completes on its
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
		a.Log.Error("autopilot: list preparing orders", "error", err)
		return
	}
	now := time.Now()
	for _, p := range preparing {
		if !domain.DueForReady(now, p.PreparingSince, a.PrepTime) {
			continue
		}
		err := MarkReady(ctx, a.Pool, "", p.OrderID)
		switch {
		case err == nil:
			a.Log.Info("autopilot marked order ready", "orderId", p.OrderID)
		case errors.Is(err, ErrNotPreparing):
			// Staff pressed the button, or it was cancelled, between list and lock.
		default:
			a.Log.Error("autopilot: mark ready", "orderId", p.OrderID, "error", err)
		}
	}
}
