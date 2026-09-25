package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

func TestPlanStops(t *testing.T) {
	here := events.Location{Lat: -22.57, Lng: 17.08}
	route := func(kinds ...string) []RequestedStop {
		out := make([]RequestedStop, len(kinds))
		for i, k := range kinds {
			out[i] = RequestedStop{Kind: k, Location: here}
		}
		return out
	}
	limits := &packs.MultiStopSettings{MaxStops: 4}
	cases := []struct {
		name string
		req  []RequestedStop
		want string
	}{
		{"multi-drop", route("pickup", "dropoff", "dropoff", "dropoff"), ""},
		{"with a waypoint", route("pickup", "waypoint", "service"), ""},
		{"round trip", route("pickup", "dropoff", "return"), ""},
		{"one stop", route("pickup"), "2-4 stops"},
		{"too many", route("pickup", "dropoff", "dropoff", "dropoff", "dropoff"), "2-4 stops"},
		{"starts empty-handed", route("dropoff", "dropoff"), "first stop must be a pickup"},
		{"ends on a pickup", route("pickup", "pickup"), "last stop must be"},
		{"ends on a waypoint", route("pickup", "waypoint"), "last stop must be"},
		{"unknown kind", route("pickup", "teleport", "dropoff"), `kind "teleport"`},
		{"no location", []RequestedStop{{Kind: "pickup", Location: here}, {Kind: "dropoff"}}, "stop 2: location"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stops, err := PlanStops(c.req, limits)
			if c.want == "" {
				if err != nil || len(stops) != len(c.req) || stops[len(stops)-1].Seq != len(c.req)-1 {
					t.Fatalf("valid route rejected: %v %+v", err, stops)
				}
				return
			}
			if !errors.Is(err, ErrBadStops) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want ErrBadStops mentioning %q, got %v", c.want, err)
			}
		})
	}
	if _, err := PlanStops(route("pickup", "dropoff"), nil); !errors.Is(err, ErrBadStops) {
		t.Fatalf("without the module: %v", err)
	}
}

func TestValidPoint(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     bool
	}{
		{"windhoek", -22.56, 17.08, true},
		{"omitted decodes to 0,0", 0, 0, false},
		{"lat out of range", 91, 17, false},
		{"lng out of range", -22, 181, false},
		{"equator is fine when lng isn't 0", 0, 17.08, true},
	}
	for _, c := range cases {
		if got := ValidPoint(events.Location{Lat: c.lat, Lng: c.lng}); got != c.want {
			t.Errorf("%s: ValidPoint = %v, want %v", c.name, got, c.want)
		}
	}
}
