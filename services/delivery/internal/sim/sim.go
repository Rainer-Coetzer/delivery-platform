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
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/domain"
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
			assigned, err := consumers.TryAssign(ctx, tx, pa.OrderID, pa.RestaurantID, pickup, dropoff)
			if err != nil {
				return err
			}
			if assigned {
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
		if d.Status == "ASSIGNED" {
			target = events.Location{Lat: d.PickupLat, Lng: d.PickupLng}
		}
		s.advance(ctx, d, target)
	}
}

func (s *LocationSimulator) advance(ctx context.Context, d *store.Delivery, target events.Location) {
	drivers, err := s.Store.ListDrivers(ctx)
	if err != nil {
		s.Log.Error("list drivers for simulation", "error", err)
		return
	}
	var current *store.Driver
	for _, dr := range drivers {
		if dr.ID == d.DriverID {
			current = dr
			break
		}
	}
	if current == nil {
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

	// Arriving at the pickup point while still ASSIGNED just means the
	// driver is now waiting at the restaurant; the actual PICKED_UP
	// transition still comes from the driver tapping "picked up" in the
	// app (see api.pickedUp), so there is nothing further to do here.
	_ = arrived
}
