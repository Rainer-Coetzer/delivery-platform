package domain

import (
	"os"
	"sort"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
)

// originalTransitions is the hardcoded state machine the food pack
// replaced (Phase C), kept as the fixture it is checked against.
var originalTransitions = map[string][]string{
	"CREATED":          {"CONFIRMED", "CANCELLED"},
	"CONFIRMED":        {"PREPARING", "CANCELLED"},
	"PREPARING":        {"READY", "CANCELLED"},
	"READY":            {"OUT_FOR_DELIVERY", "CANCELLED"},
	"OUT_FOR_DELIVERY": {"DELIVERED", "CANCELLED"},
	"DELIVERED":        {},
	"CANCELLED":        {},
}

// The food pack must reproduce the original hardcoded state machine
// exactly: same states, same edges. Role and event triggers only decide
// *when* an edge fires; the set of possible moves may not change.
func TestFoodPackMatchesOriginalStateMachine(t *testing.T) {
	data, err := os.ReadFile("../../../../packs/food/lifecycle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := lifecycle.Load(data, lifecycle.Known{
		Events: []string{"payments.completed", "payments.failed", "merchant.job-accepted", "merchant.job-rejected", "merchant.job-ready", "dispatch.stop-completed"},
		Roles:  []string{"customer", "merchant", "agent", "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}

	edges := func(pairs map[string]bool) []string {
		out := make([]string, 0, len(pairs))
		for k := range pairs {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	old := map[string]bool{}
	for from, tos := range originalTransitions {
		for _, to := range tos {
			old[from+" -> "+to] = true
		}
	}
	pack := map[string]bool{}
	for _, tr := range m.Transitions {
		pack[tr.From+" -> "+tr.To] = true
	}
	o, p := edges(old), edges(pack)
	if len(o) != len(p) {
		t.Fatalf("edge sets differ:\nold:  %v\npack: %v", o, p)
	}
	for i := range o {
		if o[i] != p[i] {
			t.Fatalf("edge sets differ:\nold:  %v\npack: %v", o, p)
		}
	}
	if len(m.States) != len(originalTransitions) {
		t.Fatalf("pack has %d states, original had %d", len(m.States), len(originalTransitions))
	}
}
