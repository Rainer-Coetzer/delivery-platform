// Package payment is the payments module: simulated authorization on job
// creation and refunds on cancellation.
package payment

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/payment/internal/store"
)

var Definition = app.Definition{
	Name:       "payment",
	Migrations: migrations.FS,
	New: func(d app.Deps) (*app.Module, error) {
		h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
		return &app.Module{
			Routes: (&api.API{Store: store.New(d.Pool), Log: d.Log, Auth: d.Auth}).Routes,
			Outbox: true,
			Consumers: []app.Consumer{{
				Group: "payment-service", Topics: []string{events.JobCreated, events.JobCancelled}, Handler: h.Handle,
			}},
		}, nil
	},
}
