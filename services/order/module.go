// Package order is the jobs module: order creation (priced from the
// restaurant catalogue) and the order state machine.
package order

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/catalogue"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/store"
)

// Definition. Schema "orders", not "order": ORDER is a reserved word in SQL,
// and an unquoted search_path of `order` would fail in the one place someone
// forgets to quote it.
var Definition = app.Definition{
	Name:       "order",
	Schema:     "orders",
	Migrations: migrations.FS,
	New:        newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	a := &api.API{
		Pool: d.Pool, Store: store.New(d.Pool), Log: d.Log, Auth: d.Auth,
		Catalogue: catalogue.New(d.URL("restaurant"), d.HTTP),
	}
	return &app.Module{
		Routes: a.Routes,
		Outbox: true,
		Consumers: []app.Consumer{{
			Group: "order-service",
			Topics: []string{
				events.RestaurantAccepted, events.RestaurantRejected, events.RestaurantReady,
				events.PaymentCompleted, events.PaymentFailed,
				events.DeliveryAssigned, events.DeliveryPickedUp, events.DeliveryCompleted,
			},
			Handler: h.Handle,
		}},
	}, nil
}
