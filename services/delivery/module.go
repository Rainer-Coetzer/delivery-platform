// Package delivery is the dispatch module: drivers, nearest-driver
// assignment, delivery progress and the demo driver simulation.
package delivery

import (
	"context"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/sim"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

var Definition = app.Definition{
	Name:       "delivery",
	Migrations: migrations.FS,
	New:        newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	autopilot, err := config.Bool("DEMO_AUTOPILOT", false)
	if err != nil {
		return nil, err
	}
	// 60m per 2s tick is a believable city speed but makes a demo order
	// take minutes; the dev compose override raises it.
	stepMeters, err := config.Float("SIM_STEP_METERS", 60)
	if err != nil {
		return nil, err
	}
	d.Log.Info("driver simulation", "autopilot", autopilot, "stepMeters", stepMeters)

	st := store.New(d.Pool)
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	retrier := &sim.PendingAssignmentRetrier{Pool: d.Pool, Store: st, Log: d.Log}
	// Location pings go straight to the bus, not the outbox: ephemeral
	// telemetry, where a lost ping is replaced two seconds later.
	simulator := &sim.LocationSimulator{Pool: d.Pool, Store: st, Producer: d.Bus, Log: d.Log, StepMeters: stepMeters, Autopilot: autopilot}
	return &app.Module{
		Routes:    (&api.API{Pool: d.Pool, Store: st, Log: d.Log, Auth: d.Auth}).Routes,
		Outbox:    true,
		Consumers: []app.Consumer{{Group: "delivery-service", Topics: consumers.Topics, Handler: h.Handle}},
		Workers:   []func(context.Context){retrier.Run, simulator.Run},
	}, nil
}
