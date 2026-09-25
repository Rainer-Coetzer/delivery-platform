package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// ErrBadStops means a customer-given route can't be run as asked.
var ErrBadStops = errors.New("route rejected")

// RequestedStop is one stop of a multi-stop route, in visiting order.
type RequestedStop struct {
	Kind     string          `json:"kind"`
	Location events.Location `json:"location"`
}

// PlanStops checks a multi-stop route and numbers it. The rules are what
// dispatch relies on: the agent starts by collecting something (the first
// stop is a pickup), and the route ends by handing it over or doing the
// work (the last stop is a dropoff, a service or a return, never a pickup
// or a mere waypoint).
func PlanStops(req []RequestedStop, limits *packs.MultiStopSettings) ([]Stop, error) {
	if limits == nil {
		return nil, fmt.Errorf("%w: this platform doesn't take multi-stop jobs", ErrBadStops)
	}
	if len(req) < 2 || len(req) > limits.MaxStops {
		return nil, fmt.Errorf("%w: a route has 2-%d stops, got %d", ErrBadStops, limits.MaxStops, len(req))
	}
	var problems []string
	out := make([]Stop, 0, len(req))
	for i, s := range req {
		n := i + 1
		if !slices.Contains(packs.StopKinds, s.Kind) {
			problems = append(problems, fmt.Sprintf("stop %d: kind %q (valid: %s)", n, s.Kind, strings.Join(packs.StopKinds, ", ")))
		}
		if !ValidPoint(s.Location) {
			problems = append(problems, fmt.Sprintf("stop %d: location must be a real coordinate", n))
		}
		out = append(out, Stop{Stop: events.Stop{Seq: i, Kind: s.Kind, Location: s.Location}})
	}
	if req[0].Kind != events.StopPickup {
		problems = append(problems, "the first stop must be a pickup")
	}
	if last := req[len(req)-1].Kind; last == events.StopPickup || last == "waypoint" {
		problems = append(problems, "the last stop must be a dropoff, service or return, not a "+last)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrBadStops, strings.Join(problems, "; "))
	}
	return out, nil
}

// ValidPoint rejects out-of-range coordinates and the 0,0 an omitted
// location decodes to (a point in the Atlantic that would send an agent
// nowhere useful).
func ValidPoint(l events.Location) bool {
	if l.Lat == 0 && l.Lng == 0 {
		return false
	}
	return l.Lat >= -90 && l.Lat <= 90 && l.Lng >= -180 && l.Lng <= 180
}
