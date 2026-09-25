// Package dispatch is the dispatch module: agents, nearest-agent
// assignment, assignment progress and the demo agent simulation.
package dispatch

import (
	"context"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/sim"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

var Definition = app.Definition{
	Name:          "dispatch",
	FormerSchemas: []string{"delivery"},
	Migrations:    migrations.FS,
	New:           newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	autopilot, err := config.Bool("DEMO_AUTOPILOT", false)
	if err != nil {
		return nil, err
	}
	// 60m per 2s tick is a believable city speed but makes a demo job
	// take minutes; the dev compose override raises it.
	stepMeters, err := config.Float("SIM_STEP_METERS", 60)
	if err != nil {
		return nil, err
	}
	d.Log.Info("agent simulation", "autopilot", autopilot, "stepMeters", stepMeters)

	st := store.New(d.Pool)
	dispatcher := &consumers.Dispatcher{Strategy: d.Strategies.Dispatch, ETA: d.Strategies.ETA}
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log, Dispatcher: dispatcher}
	retrier := &sim.PendingAssignmentRetrier{Pool: d.Pool, Store: st, Log: d.Log, Dispatcher: dispatcher}
	// Location pings go straight to the bus, not the outbox: ephemeral
	// telemetry, where a lost ping is replaced two seconds later.
	simulator := &sim.LocationSimulator{Pool: d.Pool, Store: st, Producer: d.Bus, Log: d.Log, ETA: d.Strategies.ETA, StepMeters: stepMeters, Autopilot: autopilot}
	return &app.Module{
		Routes:    (&api.API{Pool: d.Pool, Store: st, Log: d.Log, Auth: d.Auth}).Routes,
		Outbox:    true,
		Consumers: []app.Consumer{{Group: "dispatch-service", Topics: consumers.Topics, Handler: h.Handle}},
		Workers:   []func(context.Context){retrier.Run, simulator.Run},
	}, nil
}
