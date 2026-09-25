// Package jobs is the jobs module: job creation (priced from the
// merchant catalogue) and the job state machine.
package jobs

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/catalogue"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

var Definition = app.Definition{
	Name:          "jobs",
	FormerSchemas: []string{"orders"},
	Migrations:    migrations.FS,
	New:           newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	a := &api.API{
		Pool: d.Pool, Store: store.New(d.Pool), Log: d.Log, Auth: d.Auth,
		Catalogue: catalogue.New(d.URL("merchant"), d.HTTP),
	}
	return &app.Module{
		Routes: a.Routes,
		Outbox: true,
		Consumers: []app.Consumer{{
			Group: "jobs-service",
			Topics: []string{
				events.MerchantAccepted, events.MerchantRejected, events.MerchantReady,
				events.PaymentCompleted, events.PaymentFailed,
				events.DispatchAssigned, events.DispatchPickedUp, events.DispatchCompleted,
			},
			Handler: h.Handle,
		}},
	}, nil
}
