// Package restaurant is the merchants module: restaurants, menus and stock,
// accept/reject on new orders, and the kitchen's mark-ready step.
package restaurant

import (
	"fmt"
	"time"
	// Embeds the IANA tz database. Opening hours are judged in each
	// restaurant's timezone (domain.OpenAtTime), and the Alpine runtime image
	// ships no tz data. It lives here, in the module, not in a main package:
	// that way every binary that includes this module (the service or
	// cmd/platform) gets it, and it can't be lost when a main is rewritten.
	_ "time/tzdata"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/kitchen"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/store"
)

var Definition = app.Definition{
	Name:       "restaurant",
	Migrations: migrations.FS,
	New:        newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	st := store.New(d.Pool)
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	m := &app.Module{
		Routes:    (&api.API{Pool: d.Pool, Store: st, Log: d.Log, Auth: d.Auth}).Routes,
		Outbox:    true,
		Consumers: []app.Consumer{{Group: "restaurant-service", Topics: consumers.Topics, Handler: h.Handle}},
	}
	autopilot, err := config.Bool("DEMO_AUTOPILOT", false)
	if err != nil {
		return nil, err
	}
	if autopilot {
		prep, err := config.Duration("AUTOPILOT_PREP_TIME", 20*time.Second)
		if err != nil {
			return nil, fmt.Errorf("autopilot: %w", err)
		}
		d.Log.Info("demo autopilot on: orders are marked ready automatically", "prepTime", prep)
		m.Workers = append(m.Workers, (&kitchen.Autopilot{Pool: d.Pool, Store: st, Log: d.Log, PrepTime: prep}).Run)
	}
	return m, nil
}
