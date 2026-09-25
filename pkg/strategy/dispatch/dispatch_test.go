package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

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
		{"teleport", ``, `unknown dispatch strategy "teleport" (registered: capacity_aware, least_loaded, manual, nearest, open_offer)`},
		{"open_offer", `{offer_to: 0, expires_seconds: 60, within_km: 5}`, "offer_to must be >= 1"},
		{"open_offer", `{offer_to: 3, expires_seconds: 0, within_km: 5}`, "expires_seconds must be >= 1"},
		{"open_offer", `{offer_to: 3, expires_seconds: 60}`, "within_km must be > 0"},
	} {
		if _, err := Build(c.name, strategytest.Params(t, "dispatch", c.params)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: want %q, got %v", c.name, c.params, c.want, err)
		}
	}
}

func TestCapacityAware(t *testing.T) {
	s, err := Build("capacity_aware", strategytest.Params(t, "dispatch", ""))
	if err != nil {
		t.Fatal(err)
	}
	fleet := []Candidate{ // nearest first
		{ID: "truck-near", DistanceMeters: 100, CapacityGrams: 10_000_000, CapacityCm3: 40_000_000},
		{ID: "van", DistanceMeters: 900, CapacityGrams: 800_000, CapacityCm3: 6_000_000},
		{ID: "bakkie", DistanceMeters: 1200, CapacityGrams: 800_000, CapacityCm3: 3_000_000},
		{ID: "bike", DistanceMeters: 50, CapacityGrams: 15_000, CapacityCm3: 60_000},
	}
	pick := func(w, v int64) string {
		c, ok := s.Pick(context.Background(), Request{WeightGrams: w, VolumeCm3: v}, fleet)
		if !ok {
			return ""
		}
		return c.ID
	}
	if got := pick(500_000, 2_000_000); got != "bakkie" {
		t.Fatalf("half a ton: the tightest fit (same weight spare as the van, less volume) is the bakkie, got %s", got)
	}
	if got := pick(5_000, 20_000); got != "bike" {
		t.Fatalf("a parcel goes by bike, not truck: got %s", got)
	}
	if got := pick(20_000_000, 1); got != "" {
		t.Fatalf("nothing carries 20 t: got %s", got)
	}
}

func TestManualNeverPicks(t *testing.T) {
	if id, ok := pick(t, "manual", "", candidates); ok {
		t.Fatalf("manual picked %s", id)
	}
}

func TestOpenOffer(t *testing.T) {
	s, err := Build("open_offer", strategytest.Params(t, "dispatch", `{offer_to: 2, expires_seconds: 45, within_km: 3}`))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := s.(Offerer)
	if !ok {
		t.Fatal("open_offer must be an Offerer")
	}
	if _, picked := o.Pick(context.Background(), Request{}, candidates); picked {
		t.Fatal("open_offer never picks: the agents accept")
	}
	got := o.Offer(context.Background(), Request{}, candidates)
	if len(got) != 2 || got[0].ID != "near-busy" || got[1].ID != "mid-idle" || o.OfferFor() != 45*time.Second {
		t.Fatalf("offers to the 2 nearest in range: %+v, for %v", got, o.OfferFor())
	}
	if got := o.Offer(context.Background(), Request{}, candidates[3:]); len(got) != 0 {
		t.Fatalf("nobody in range, nobody asked: %+v", got)
	}
}
