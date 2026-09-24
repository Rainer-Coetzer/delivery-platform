// Package admin is the reporting module: a read model built from events,
// and admin-only reports over it.
package admin

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/admin/internal/store"
)

var Definition = app.Definition{
	Name:       "admin",
	Migrations: migrations.FS,
	New: func(d app.Deps) (*app.Module, error) {
		h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
		return &app.Module{
			Routes: (&api.API{Store: store.New(d.Pool), Auth: d.Auth}).Routes,
			Consumers: []app.Consumer{{
				Group:   "admin-service",
				Topics:  []string{events.OrderCreated, events.OrderStatusChanged, events.OrderCancelled, events.DeliveryAssigned},
				Handler: h.Handle,
			}},
		}, nil
	},
}
