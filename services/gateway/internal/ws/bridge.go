package ws

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

// Bridge forwards orders.status-changed and delivery.location events to the
// WebSocket room named by their orderId, so a client watching one order sees
// its status change and its driver move without any polling.
type Bridge struct {
	Hub *Hub
	Log *slog.Logger
}

func (b *Bridge) Handle(ctx context.Context, env events.Envelope) error {
	room := env.OrderID
	if room == "" {
		// delivery.location is keyed by driverId at the Kafka level but
		// still carries orderId in its payload; recover it there.
		data, err := events.Decode[events.DriverLocationData](env)
		if err != nil || data.OrderID == "" {
			return nil
		}
		room = data.OrderID
	}
	payload, err := json.Marshal(env)
	if err != nil {
		b.Log.Error("marshal event for websocket broadcast", "error", err)
		return nil
	}
	b.Hub.Broadcast(room, payload)
	return nil
}
