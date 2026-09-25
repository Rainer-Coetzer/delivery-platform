// Package reporting is the reporting module: a read model built from events,
// and admin-only reports over it.
package reporting

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/reporting/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/reporting/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/reporting/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/reporting/internal/store"
)

var Definition = app.Definition{
	Name:          "reporting",
	FormerSchemas: []string{"admin"},
	Migrations:    migrations.FS,
	New: func(d app.Deps) (*app.Module, error) {
		h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
		return &app.Module{
			Routes: (&api.API{Store: store.New(d.Pool), Auth: d.Auth}).Routes,
			Consumers: []app.Consumer{{
				Group:   "reporting-service",
				Topics:  []string{events.JobCreated, events.JobStatusChanged, events.DispatchAssigned},
				Handler: h.Handle,
			}},
		}, nil
	},
}
