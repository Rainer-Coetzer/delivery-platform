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
	env, err := forClients(env)
	if err != nil {
		b.Log.Error("trim event for websocket broadcast", "topic", env.EventType, "error", err)
		return nil
	}
	payload, err := json.Marshal(env)
	if err != nil {
		b.Log.Error("marshal event for websocket broadcast", "error", err)
		return nil
	}
	b.Hub.Broadcast(room, payload)
	return nil
}

// forClients trims an event to what a watcher may see. The step into
// ready_for_dispatch carries the job's stops for dispatch, and with them
// PIN hashes: a 4-digit PIN falls out of its hash in milliseconds, so an
// agent watching the job could skip asking the customer. Watchers get the
// state change only; the job itself (GET /jobs/{id}) shows each caller
// what they may see.
func forClients(env events.Envelope) (events.Envelope, error) {
	if env.EventType != events.JobStatusChanged {
		return env, nil
	}
	d, err := events.Decode[events.JobStatusChangedData](env)
	if err != nil {
		return env, err
	}
	d.Stops, d.Size, d.Requires = nil, nil, nil
	env.Data, err = json.Marshal(d)
	return env, err
}
