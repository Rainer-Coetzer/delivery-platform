// Package consumers turns every order/payment/delivery event into a
// human-readable notification for the customer, restaurant or driver
// involved, and logs it to the console and an "email" stub — real channels
// would plug in here without touching any other service.
package consumers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/store"
)

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	n, err := build(env)
	if err != nil {
		return err
	}
	if n == nil {
		return nil // event we don't notify on, e.g. an unrecognized topic
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		n.ID = uuid.NewString()
		if err := store.Insert(ctx, tx, n); err != nil {
			return err
		}
		h.Log.Info("notification", "recipient", n.Recipient, "title", n.Title, "body", n.Body, "channel", "console")
		h.Log.Info("notification (email stub)", "recipient", n.Recipient, "title", n.Title)
		return nil
	})
}

// build maps an event to the notification it produces, or nil if this
// service does not notify on that topic (e.g. the high-frequency
// delivery.location stream).
func build(env events.Envelope) (*store.Notification, error) {
	switch env.EventType {
	case events.OrderCreated:
		d, err := events.Decode[events.OrderCreatedData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{
			Recipient: "restaurant:" + d.RestaurantID, OrderID: d.OrderID, Channel: "in-app",
			Title: "New order", Body: fmt.Sprintf("Order %s from %s, total N$%.2f", short(d.OrderID), d.CustomerName, cents(d.TotalCents)),
		}, nil
	case events.OrderStatusChanged:
		d, err := events.Decode[events.OrderStatusChangedData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{
			Recipient: "user:" + d.CustomerID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Order update", Body: fmt.Sprintf("Order %s is now %s", short(d.OrderID), d.To),
		}, nil
	case events.OrderCancelled:
		d, err := events.Decode[events.OrderCancelledData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{
			Recipient: "user:" + d.CustomerID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Order cancelled", Body: fmt.Sprintf("Order %s was cancelled: %s", short(d.OrderID), d.Reason),
		}, nil
	case events.RestaurantAccepted:
		d, err := events.Decode[events.RestaurantDecisionData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "order:" + d.OrderID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Order accepted", Body: "The restaurant accepted order " + short(d.OrderID)}, nil
	case events.RestaurantRejected:
		d, err := events.Decode[events.RestaurantDecisionData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "order:" + d.OrderID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Order rejected", Body: "The restaurant rejected order " + short(d.OrderID) + ": " + d.Reason}, nil
	case events.PaymentFailed:
		d, err := events.Decode[events.PaymentData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "order:" + d.OrderID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Payment failed", Body: "Payment for order " + short(d.OrderID) + " failed: " + d.Reason}, nil
	case events.DeliveryAssigned:
		d, err := events.Decode[events.DeliveryAssignedData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "order:" + d.OrderID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Driver assigned", Body: fmt.Sprintf("%s is on the way, ETA %d min", d.DriverName, d.EtaMinutes)}, nil
	case events.DeliveryCompleted:
		d, err := events.Decode[events.DeliveryProgressData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "order:" + d.OrderID, OrderID: d.OrderID, Channel: "in-app",
			Title: "Delivered", Body: "Order " + short(d.OrderID) + " has been delivered, enjoy!"}, nil
	default:
		return nil, nil
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func cents(c int64) float64 { return float64(c) / 100 }
