// Package notification is the notifications module: one inbox entry (and a
// console/email stub) per domain event.
package notification

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/store"
)

var Definition = app.Definition{
	Name:       "notification",
	Migrations: migrations.FS,
	New: func(d app.Deps) (*app.Module, error) {
		// Every topic except the high-frequency dispatch.location stream.
		topics := make([]string, 0, len(events.AllTopics))
		for _, t := range events.AllTopics {
			if t != events.DispatchLocation {
				topics = append(topics, t)
			}
		}
		h := &consumers.Handlers{Pool: d.Pool, Log: d.Log, Channels: d.Strategies.Notify, Words: d.Pack, Money: d.Brand.FormatMoney}
		return &app.Module{
			Routes:    (&api.API{Store: store.New(d.Pool), Auth: d.Auth}).Routes,
			Consumers: []app.Consumer{{Group: "notification-service", Topics: topics, Handler: h.Handle}},
		}, nil
	},
}
