package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
)

// Nearest first, as the dispatch module supplies them.
var candidates = []Candidate{
	{ID: "near-busy", DistanceMeters: 500, AssignmentsToday: 6},
	{ID: "mid-idle", DistanceMeters: 2500, AssignmentsToday: 1},
	{ID: "mid-idle-too", DistanceMeters: 2600, AssignmentsToday: 1},
	{ID: "far-idle", DistanceMeters: 9000, AssignmentsToday: 0},
}

func pick(t *testing.T, name, params string, cs []Candidate) (string, bool) {
	t.Helper()
	s, err := Build(name, strategytest.Params(t, "dispatch", params))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s.Pick(context.Background(), Request{JobID: "j1"}, cs)
	return c.ID, ok
}

func TestNearest(t *testing.T) {
	if id, ok := pick(t, "nearest", "", candidates); !ok || id != "near-busy" {
		t.Fatalf("got %s %v", id, ok)
	}
	if _, ok := pick(t, "nearest", "", nil); ok {
		t.Fatal("nobody free: no pick")
	}
}

func TestLeastLoaded(t *testing.T) {
	// Within 3 km: the idle agent wins over the busy nearer one, and of two
	// equally idle agents the nearer one wins. The idlest agent is too far.
	if id, ok := pick(t, "least_loaded", `{within_km: 3}`, candidates); !ok || id != "mid-idle" {
		t.Fatalf("got %s %v", id, ok)
	}
	if _, ok := pick(t, "least_loaded", `{within_km: 0.1}`, candidates); ok {
		t.Fatal("nobody within range: no pick, the job waits")
	}
}

func TestBootErrors(t *testing.T) {
	for _, c := range []struct{ name, params, want string }{
		{"least_loaded", `{}`, "within_km must be > 0"},
		{"nearest", `{radius: 3}`, "field radius not found"},
		{"open_offer", ``, `unknown dispatch strategy "open_offer" (registered: least_loaded, nearest)`},
	} {
		if _, err := Build(c.name, strategytest.Params(t, "dispatch", c.params)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: want %q, got %v", c.name, c.params, c.want, err)
		}
	}
}
