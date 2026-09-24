// Package sim runs two background loops for the Delivery service:
// PendingAssignmentRetrier periodically retries orders that had no free
// driver when they became ready, and LocationSimulator moves each assigned
// driver toward its current target (the restaurant, then the customer) and
// publishes delivery.location — the built-in stand-in for real GPS so the
// map has something to show without a physical driver.
package sim

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

type PendingAssignmentRetrier struct {
	Pool     *db.Pool
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
}

func (p *PendingAssignmentRetrier) Run(ctx context.Context) {
	if p.Interval == 0 {
		p.Interval = 5 * time.Second
	}
	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *PendingAssignmentRetrier) tick(ctx context.Context) {
	pending, err := p.Store.ListPending(ctx)
	if err != nil {
		p.Log.Error("list pending assignments", "error", err)
		return
	}
	for _, pa := range pending {
		err := p.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			pickup := events.Location{Lat: pa.PickupLat, Lng: pa.PickupLng}
			dropoff := events.Location{Lat: pa.DropoffLat, Lng: pa.DropoffLng}
			outcome, err := consumers.TryAssign(ctx, tx, pa.OrderID, pa.RestaurantID, pickup, dropoff)
			if err != nil {
				return err
			}
			// Assigned: done. Cancelled: the cancel handler normally deletes
			// the row itself, but drop it here too in case it was queued
			// after the cancel landed. NoDriverFree: leave it for next tick.
			if outcome == consumers.Assigned || outcome == consumers.OrderCancelled {
				return store.DeletePending(ctx, tx, pa.OrderID)
			}
			return nil
		})
		if err != nil {
			p.Log.Error("retry pending assignment", "orderId", pa.OrderID, "error", err)
		}
	}
}

type LocationSimulator struct {
	Pool     *db.Pool
	Store    *store.Store
	Producer outbox.Publisher
	Log      *slog.Logger
	Interval time.Duration
	// StepMeters is how far a driver moves toward its target per tick.
	StepMeters float64
	// Autopilot (DEMO_AUTOPILOT) makes a simulated driver pick up when it
	// reaches the restaurant and complete when it reaches the customer, so
	// an order finishes with nobody tapping buttons. Off by default: with
	// it off, arriving only means the driver is waiting for a real tap.
	Autopilot bool
}

func (s *LocationSimulator) Run(ctx context.Context) {
	if s.Interval == 0 {
		s.Interval = 2 * time.Second
	}
	if s.StepMeters == 0 {
		s.StepMeters = 60 // roughly the distance an 8 m/s driver covers in a 2s tick, exaggerated for a visible demo
	}
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *LocationSimulator) tick(ctx context.Context) {
	deliveries, err := s.Store.ListActive(ctx)
	if err != nil {
		s.Log.Error("list active deliveries for simulation", "error", err)
		return
	}
	for _, d := range deliveries {
		target := events.Location{Lat: d.DropoffLat, Lng: d.DropoffLng}
		if d.Status == lifecycle.StatusAssigned {
			target = events.Location{Lat: d.PickupLat, Lng: d.PickupLng}
		}
		s.advance(ctx, d, target)
	}
}

func (s *LocationSimulator) advance(ctx context.Context, d *store.Delivery, target events.Location) {
	// One lookup per delivery. This used to list every driver per delivery
	// per tick (audit P1, O(drivers x deliveries)).
	current, err := s.Store.GetDriver(ctx, d.DriverID)
	if err != nil {
		s.Log.Error("load driver for simulation", "driverId", d.DriverID, "error", err)
		return
	}

	newLat, newLng, arrived := domain.StepToward(current.Lat, current.Lng, target.Lat, target.Lng, s.StepMeters)
	if err := store.UpdateDriverLocation(ctx, s.Pool, d.DriverID, newLat, newLng); err != nil {
		s.Log.Error("simulate driver movement", "driverId", d.DriverID, "error", err)
		return
	}

	eta := domain.ETAMinutes(domain.HaversineMeters(newLat, newLng, target.Lat, target.Lng))
	if err := store.UpdateETA(ctx, s.Pool, d.ID, eta); err != nil {
		s.Log.Error("update eta", "error", err)
	}

	env, err := events.New(events.DeliveryLocation, d.OrderID, events.DriverLocationData{
		DriverID: d.DriverID, OrderID: d.OrderID, Lat: newLat, Lng: newLng, EtaMinutes: eta,
	})
	if err != nil {
		s.Log.Error("build location event", "error", err)
		return
	}
	payload, err := json.Marshal(env)
	if err != nil {
		s.Log.Error("marshal location envelope", "error", err)
		return
	}
	if err := s.Producer.Publish(ctx, env.EventType, d.DriverID, payload); err != nil {
		s.Log.Error("publish driver location", "error", err)
	}

	if arrived && s.Autopilot {
		s.autopilot(ctx, d)
	}
}

// autopilot performs the tap a real driver would make on arrival. Losing a
// race to a real driver (ErrWrongState) is fine and not worth an error log.
func (s *LocationSimulator) autopilot(ctx context.Context, d *store.Delivery) {
	from, to := lifecycle.StatusPickedUp, lifecycle.StatusCompleted
	if d.Status == lifecycle.StatusAssigned {
		from, to = lifecycle.StatusAssigned, lifecycle.StatusPickedUp
	}
	err := lifecycle.Advance(ctx, s.Pool, d.ID, from, to)
	switch {
	case err == nil:
		s.Log.Info("autopilot advanced delivery", "orderId", d.OrderID, "to", to)
	case errors.Is(err, lifecycle.ErrWrongState):
	default:
		s.Log.Error("autopilot advance failed", "orderId", d.OrderID, "to", to, "error", err)
	}
}
