// Package dispatch is the dispatch strategy kind: which free agent gets a
// job. The dispatch module finds the candidates (free agents, nearest
// first) and locks the one the strategy picks; the strategy only chooses.
//
// Built-ins: nearest, least_loaded, capacity_aware, manual, open_offer.
// With the capacity module on, the dispatch module only offers agents
// whose vehicle fits, whatever the strategy; capacity_aware then chooses
// the tightest fit.
package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Candidate is a free agent the job could go to.
type Candidate struct {
	ID               string
	Vehicle          string
	DistanceMeters   float64 // to the pickup
	AssignmentsToday int
	CapacityGrams    int64 // 0 when not declared (only with the capacity module off)
	CapacityCm3      int64
}

// Request is the job being dispatched.
type Request struct {
	JobID       string
	WeightGrams int64 // the job's packages, 0 without the packages module
	VolumeCm3   int64
}

// Strategy picks one candidate. Candidates come nearest first; ok=false
// means none is suitable and the job waits for the next retry.
type Strategy interface {
	Pick(ctx context.Context, req Request, candidates []Candidate) (c Candidate, ok bool)
}

// Offerer is a strategy that doesn't pick: it offers the job to several
// agents for a while, and the first to accept gets it (open_offer). The
// dispatch module sees the interface and runs the offer instead of Pick.
type Offerer interface {
	Strategy
	Offer(ctx context.Context, req Request, candidates []Candidate) []Candidate
	OfferFor() time.Duration
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
	Register("capacity_aware", newCapacityAware)
	Register("manual", newManual)
	Register("open_offer", newOpenOffer)
}

// --- manual: a person assigns every job ---

// manual never picks. Every ready job waits in the queue until an admin
// assigns it (POST /assignments), for operators who dispatch by hand.
type manual struct{}

func newManual(p strategy.Params) (Strategy, error) {
	if err := p.Decode(&struct{}{}); err != nil {
		return nil, err
	}
	return manual{}, nil
}

func (manual) Pick(context.Context, Request, []Candidate) (Candidate, bool) {
	return Candidate{}, false
}

// --- open_offer: the first agent to accept gets it ---

type openOffer struct {
	OfferTo        int     `yaml:"offer_to"`        // how many agents see the offer at once
	ExpiresSeconds int     `yaml:"expires_seconds"` // how long before it goes to the next ones
	WithinKm       float64 `yaml:"within_km"`       // nobody further than this is asked
}

func newOpenOffer(p strategy.Params) (Strategy, error) {
	o := &openOffer{}
	if err := p.Decode(o); err != nil {
		return nil, err
	}
	switch {
	case o.OfferTo < 1:
		return nil, fmt.Errorf("offer_to must be >= 1, got %d", o.OfferTo)
	case o.ExpiresSeconds < 1:
		return nil, fmt.Errorf("expires_seconds must be >= 1, got %d", o.ExpiresSeconds)
	case o.WithinKm <= 0:
		return nil, fmt.Errorf("within_km must be > 0, got %v", o.WithinKm)
	}
	return o, nil
}

// Pick never chooses: the agents do, by accepting.
func (o *openOffer) Pick(context.Context, Request, []Candidate) (Candidate, bool) {
	return Candidate{}, false
}

// Offer goes to the nearest OfferTo agents within range.
func (o *openOffer) Offer(_ context.Context, _ Request, cs []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cs {
		if c.DistanceMeters <= o.WithinKm*1000 && len(out) < o.OfferTo {
			out = append(out, c)
		}
	}
	return out
}

func (o *openOffer) OfferFor() time.Duration { return time.Duration(o.ExpiresSeconds) * time.Second }

// --- capacity_aware: the smallest vehicle that fits ---

// capacityAware keeps big vehicles free for big loads: of the agents that
// fit (the dispatch module has filtered them), it picks the one with the
// least weight capacity left over, then the least volume, then the
// nearest. It needs the capacity module (checked at boot).
type capacityAware struct{}

func newCapacityAware(p strategy.Params) (Strategy, error) {
	if err := p.Decode(&struct{}{}); err != nil {
		return nil, err
	}
	return capacityAware{}, nil
}

func (capacityAware) Pick(_ context.Context, req Request, cs []Candidate) (Candidate, bool) {
	var best Candidate
	found := false
	for _, c := range cs {
		if c.CapacityGrams < req.WeightGrams || c.CapacityCm3 < req.VolumeCm3 {
			continue // belt and braces: the module filters these out already
		}
		spareG, spareV := c.CapacityGrams-req.WeightGrams, c.CapacityCm3-req.VolumeCm3
		bestG, bestV := best.CapacityGrams-req.WeightGrams, best.CapacityCm3-req.VolumeCm3
		// Candidates are nearest first, so strict < keeps the nearer on a tie.
		if !found || spareG < bestG || (spareG == bestG && spareV < bestV) {
			best, found = c, true
		}
	}
	return best, found
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
