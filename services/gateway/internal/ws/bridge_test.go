package ws

import (
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

// Watchers must never get PIN hashes (or the rest of dispatch's routing
// data) from the step into ready_for_dispatch.
func TestForClientsStripsRoutingData(t *testing.T) {
	env, err := events.New(events.JobStatusChanged, "j1", events.JobStatusChangedData{
		JobID: "j1", From: "PREPARING", To: "READY", ToMilestone: "ready_for_dispatch",
		Stops:    []events.Stop{{Seq: 1, Kind: "dropoff", Proof: "pin", PinHash: events.PinHash("j1", 1, "0427")}},
		Size:     &events.JobSize{WeightGrams: 5},
		Requires: []string{"cold_chain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := forClients(env)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(out.Data)
	for _, leak := range []string{"pinHash", "stops", "size", "requires", "cold_chain"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("%q reached a watcher: %s", leak, raw)
		}
	}
	if !strings.Contains(raw, `"to":"READY"`) {
		t.Fatalf("the state change itself must survive: %s", raw)
	}

	loc, _ := events.New(events.DispatchLocation, "", events.AgentLocationData{AgentID: "a1", JobID: "j1", Lat: 1})
	if same, _ := forClients(loc); string(same.Data) != string(loc.Data) {
		t.Fatal("other events pass through untouched")
	}
}
