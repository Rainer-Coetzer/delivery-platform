package consumers

import (
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

func TestBuild(t *testing.T) {
	env := func(topic string, data any) events.Envelope {
		e, err := events.New(topic, "j1", data)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	cases := []struct {
		name      string
		env       events.Envelope
		recipient string // "" = no notification
	}{
		{"new job tells the merchant", env(events.JobCreated, events.JobCreatedData{JobID: "j1", MerchantID: "m1"}), "merchant:m1"},
		{"a courier job has no merchant to tell", env(events.JobCreated, events.JobCreatedData{JobID: "j1"}), ""},
		{"status change tells the customer", env(events.JobStatusChanged, events.JobStatusChangedData{JobID: "j1", CustomerID: "c1", To: "READY"}), "user:c1"},
		{"location pings are not notifications", env(events.DispatchLocation, events.AgentLocationData{AgentID: "a1"}), ""},
	}
	for _, c := range cases {
		n, err := build(c.env)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := ""
		if n != nil {
			got = n.Recipient
		}
		if got != c.recipient {
			t.Errorf("%s: recipient %q, want %q", c.name, got, c.recipient)
		}
	}
}
