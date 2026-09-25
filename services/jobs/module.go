// Package jobs is the jobs module: job creation (priced from the
// merchant catalogue) and running each job through the pack's lifecycle.
package jobs

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
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
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log, Lifecycle: d.Pack.Lifecycle, Requires: d.Pack.Job.Requires}
	a := &api.API{
		Pool: d.Pool, Store: store.New(d.Pool), Log: d.Log, Auth: d.Auth, Pack: d.Pack,
		Pricing: d.Strategies.Pricing, Payments: d.Strategies.Payments, Blobs: d.Blobs,
		InArea:    d.Brand.InServiceArea,
		Catalogue: catalogue.New(d.URL("merchant"), d.HTTP),
	}
	return &app.Module{
		Routes:    a.Routes,
		Outbox:    true,
		Consumers: []app.Consumer{{Group: "jobs-service", Topics: h.Topics(), Handler: h.Handle}},
	}, nil
}
