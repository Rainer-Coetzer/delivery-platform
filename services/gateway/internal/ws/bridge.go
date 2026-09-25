package ws

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

// Bridge forwards jobs.status-changed and dispatch.location events to the
// WebSocket room named by their jobId, so a client watching one job sees
// its status change and its agent move without any polling.
type Bridge struct {
	Hub *Hub
	Log *slog.Logger
}

func (b *Bridge) Handle(ctx context.Context, env events.Envelope) error {
	room := env.JobID
	if room == "" {
		// dispatch.location is keyed by agentId at the Kafka level but
		// still carries jobId in its payload; recover it there.
		data, err := events.Decode[events.AgentLocationData](env)
		if err != nil || data.JobID == "" {
			return nil
		}
		room = data.JobID
	}
	payload, err := json.Marshal(env)
	if err != nil {
		b.Log.Error("marshal event for websocket broadcast", "error", err)
		return nil
	}
	b.Hub.Broadcast(room, payload)
	return nil
}
