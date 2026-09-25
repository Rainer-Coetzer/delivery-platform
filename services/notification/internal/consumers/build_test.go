package consumers

import (
	"strings"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
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
		n, err := build(c.env, foodWords(t))
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

func foodWords(t *testing.T) Words {
	t.Helper()
	p, err := packs.Load(packfs.FS, "food", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Texts speak the pack's language: food customers get orders and drivers,
// courier customers deliveries and couriers, and state labels, not names.
func TestTextsUseThePacksWords(t *testing.T) {
	courier, err := packs.Load(packfs.FS, "courier", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := events.New(events.JobStatusChanged, "j1", events.JobStatusChangedData{JobID: "j1", CustomerID: "c1", To: "OUT_FOR_DELIVERY"})
	n, _ := build(status, foodWords(t))
	if n.Title != "Order update" || !strings.Contains(n.Body, "On the way") || strings.Contains(n.Body, "OUT_FOR_DELIVERY") {
		t.Fatalf("food: %+v", n)
	}
	assigned, _ := events.New(events.DispatchAssigned, "j1", events.AssignedData{JobID: "j1", AgentName: "Maria"})
	if n, _ := build(assigned, courier); n.Title != "Courier assigned" {
		t.Fatalf("courier: %+v", n)
	}
	status2, _ := events.New(events.JobStatusChanged, "j1", events.JobStatusChangedData{JobID: "j1", CustomerID: "c1", To: "FINDING_COURIER"})
	if n, _ := build(status2, courier); n.Title != "Delivery update" || !strings.Contains(n.Body, "Finding a courier") {
		t.Fatalf("courier: %+v", n)
	}
}
