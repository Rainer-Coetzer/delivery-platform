// Package dispatch is the dispatch strategy kind: which free agent gets a
// job. The dispatch module finds the candidates (free agents, nearest
// first) and locks the one the strategy picks; the strategy only chooses.
//
// Built-ins: nearest, least_loaded. Others named in ADR-0003 need data or
// APIs that arrive with their modules: capacity_aware (capacity), manual
// and open_offer (an assign/accept API). They are not registered until
// they work.
package dispatch

import (
	"context"
	"fmt"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Candidate is a free agent the job could go to.
type Candidate struct {
	ID               string
	Vehicle          string
	DistanceMeters   float64 // to the pickup
	AssignmentsToday int
}

// Request is the job being dispatched.
type Request struct {
	JobID string
}

// Strategy picks one candidate. Candidates come nearest first; ok=false
// means none is suitable and the job waits for the next retry.
type Strategy interface {
	Pick(ctx context.Context, req Request, candidates []Candidate) (c Candidate, ok bool)
}

var registry = strategy.NewRegistry[Strategy]("dispatch")

func Register(name string, f strategy.Factory[Strategy]) { registry.Register(name, f) }
func Build(name string, p strategy.Params) (Strategy, error) {
	return registry.Build(name, p)
}
func Names() []string { return registry.Names() }

func init() {
	Register("nearest", newNearest)
	Register("least_loaded", newLeastLoaded)
}

// --- nearest: the closest free agent ---

type nearest struct{}

func newNearest(p strategy.Params) (Strategy, error) {
	if err := p.Decode(&struct{}{}); err != nil {
		return nil, err
	}
	return nearest{}, nil
}

func (nearest) Pick(_ context.Context, _ Request, cs []Candidate) (Candidate, bool) {
	if len(cs) == 0 {
		return Candidate{}, false
	}
	return cs[0], true
}

// --- least_loaded: spread work across agents ---

// leastLoaded gives the job to whoever has had the fewest assignments
// today, among agents within WithinKm of the pickup (nearest breaks ties).
// Without the radius, a quiet agent across town would win every job.
type leastLoaded struct {
	WithinKm float64 `yaml:"within_km"`
}

func newLeastLoaded(p strategy.Params) (Strategy, error) {
	l := &leastLoaded{}
	if err := p.Decode(l); err != nil {
		return nil, err
	}
	if l.WithinKm <= 0 {
		return nil, fmt.Errorf("within_km must be > 0, got %v", l.WithinKm)
	}
	return l, nil
}

func (l *leastLoaded) Pick(_ context.Context, _ Request, cs []Candidate) (Candidate, bool) {
	var best Candidate
	found := false
	for _, c := range cs {
		if c.DistanceMeters > l.WithinKm*1000 {
			continue
		}
		// Candidates are nearest first, so a strict < keeps the nearer
		// agent on a tie.
		if !found || c.AssignmentsToday < best.AssignmentsToday {
			best, found = c, true
		}
	}
	return best, found
}
