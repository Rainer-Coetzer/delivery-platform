// Package sim runs two background loops for the Assignment service:
// PendingAssignmentRetrier periodically retries jobs that had no free
// agent when they became ready, and LocationSimulator moves each assigned
// agent toward its current target (the merchant, then the customer) and
// publishes dispatch.location — the built-in stand-in for real GPS so the
// map has something to show without a physical agent.
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
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
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
			outcome, err := consumers.TryAssign(ctx, tx, pa.JobID, pa.MerchantID, pickup, dropoff)
			if err != nil {
				return err
			}
			// Assigned: done. Cancelled: the cancel handler normally deletes
			// the row itself, but drop it here too in case it was queued
			// after the cancel landed. NoAgentFree: leave it for next tick.
			if outcome == consumers.Assigned || outcome == consumers.JobCancelled {
				return store.DeletePending(ctx, tx, pa.JobID)
			}
			return nil
		})
		if err != nil {
			p.Log.Error("retry pending assignment", "jobId", pa.JobID, "error", err)
		}
	}
}

type LocationSimulator struct {
	Pool     *db.Pool
	Store    *store.Store
	Producer outbox.Publisher
	Log      *slog.Logger
	Interval time.Duration
	// StepMeters is how far an agent moves toward its target per tick.
	StepMeters float64
	// Autopilot (DEMO_AUTOPILOT) makes a simulated agent pick up when it
	// reaches the merchant and complete when it reaches the customer, so
	// a job finishes with nobody tapping buttons. Off by default: with
	// it off, arriving only means the agent is waiting for a real tap.
	Autopilot bool
}

func (s *LocationSimulator) Run(ctx context.Context) {
	if s.Interval == 0 {
		s.Interval = 2 * time.Second
	}
	if s.StepMeters == 0 {
		s.StepMeters = 60 // roughly the distance an 8 m/s agent covers in a 2s tick, exaggerated for a visible demo
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
	assignments, err := s.Store.ListActive(ctx)
	if err != nil {
		s.Log.Error("list active assignments for simulation", "error", err)
		return
	}
	for _, d := range assignments {
		target := events.Location{Lat: d.DropoffLat, Lng: d.DropoffLng}
		if d.Status == progress.StatusAssigned {
			target = events.Location{Lat: d.PickupLat, Lng: d.PickupLng}
		}
		s.advance(ctx, d, target)
	}
}

func (s *LocationSimulator) advance(ctx context.Context, d *store.Assignment, target events.Location) {
	// One lookup per assignment. This used to list every agent per assignment
	// per tick (audit P1, O(agents x assignments)).
	current, err := s.Store.GetAgent(ctx, d.AgentID)
	if err != nil {
		s.Log.Error("load agent for simulation", "agentId", d.AgentID, "error", err)
		return
	}

	newLat, newLng, arrived := domain.StepToward(current.Lat, current.Lng, target.Lat, target.Lng, s.StepMeters)
	if err := store.UpdateAgentLocation(ctx, s.Pool, d.AgentID, newLat, newLng); err != nil {
		s.Log.Error("simulate agent movement", "agentId", d.AgentID, "error", err)
		return
	}

	eta := domain.ETAMinutes(domain.HaversineMeters(newLat, newLng, target.Lat, target.Lng))
	if err := store.UpdateETA(ctx, s.Pool, d.ID, eta); err != nil {
		s.Log.Error("update eta", "error", err)
	}

	env, err := events.New(events.DispatchLocation, d.JobID, events.AgentLocationData{
		AgentID: d.AgentID, JobID: d.JobID, Lat: newLat, Lng: newLng, EtaMinutes: eta,
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
	if err := s.Producer.Publish(ctx, env.EventType, d.AgentID, payload); err != nil {
		s.Log.Error("publish agent location", "error", err)
	}

	if arrived && s.Autopilot {
		s.autopilot(ctx, d)
	}
}

// autopilot performs the tap a real agent would make on arrival. Losing a
// race to a real agent (ErrWrongState) is fine and not worth an error log.
func (s *LocationSimulator) autopilot(ctx context.Context, d *store.Assignment) {
	from, to := progress.StatusPickedUp, progress.StatusCompleted
	if d.Status == progress.StatusAssigned {
		from, to = progress.StatusAssigned, progress.StatusPickedUp
	}
	err := progress.Advance(ctx, s.Pool, d.ID, from, to)
	switch {
	case err == nil:
		s.Log.Info("autopilot advanced assignment", "jobId", d.JobID, "to", to)
	case errors.Is(err, progress.ErrWrongState):
	default:
		s.Log.Error("autopilot advance failed", "jobId", d.JobID, "to", to, "error", err)
	}
}
