// Package pricing is the pricing strategy kind: how a job's fee is worked
// out. Money is integer cents everywhere; percentages are whole numbers
// (120 = 1.2x), so no fare ever depends on float rounding.
//
// Built-ins: flat, per_km, per_kg, zone_table, surge_tiers. A pack picks one
// in pack.yaml (`strategies.pricing`); a buyer adds their own with Register.
package pricing

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/geo"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Input is what a policy may price on. Not every policy uses every field.
type Input struct {
	SubtotalCents int64         // the job's lines (catalogue prices), before the fee
	Stops         []events.Stop // in order; distance is the straight-line path through them
	WeightGrams   int64         // total package weight (packages module)
	Load          Load          // for surge
}

// Load is how busy the platform is right now.
type Load struct {
	OpenJobs   int
	FreeAgents int
}

// Quote is a priced job. TotalCents = SubtotalCents + FeeCents.
type Quote struct {
	SubtotalCents int64
	FeeCents      int64
	TotalCents    int64
	MultiplierPct int // 100 when no surge applied
}

// Policy prices a job.
type Policy interface {
	Price(ctx context.Context, in Input) (Quote, error)
}

// ErrNoFare: the policy has no price for this job (a zone table with no
// entry for the route). The API answers 400 with the message.
var ErrNoFare = errors.New("no fare for this job")

var registry = strategy.NewRegistry[Policy]("pricing")

// Register adds a pricing policy under name.
func Register(name string, f strategy.Factory[Policy]) { registry.Register(name, f) }

// Build makes the named policy from its params.
func Build(name string, p strategy.Params) (Policy, error) { return registry.Build(name, p) }

// Names lists the registered policies.
func Names() []string { return registry.Names() }

// DistanceMeters is the straight-line length of the path through the stops.
func DistanceMeters(stops []events.Stop) float64 {
	var d float64
	for i := 1; i < len(stops); i++ {
		a, b := stops[i-1].Location, stops[i].Location
		d += geo.HaversineMeters(a.Lat, a.Lng, b.Lat, b.Lng)
	}
	return d
}

func quote(in Input, fee int64, pct int) Quote {
	return Quote{SubtotalCents: in.SubtotalCents, FeeCents: fee, TotalCents: in.SubtotalCents + fee, MultiplierPct: pct}
}

// pctOf is v * pct / 100, rounded half up, in integers.
func pctOf(v int64, pct int) int64 { return (v*int64(pct) + 50) / 100 }

func init() {
	Register("flat", newFlat)
	Register("per_km", newPerKm)
	Register("per_kg", newPerKg)
	Register("zone_table", newZoneTable)
	Register("surge_tiers", newSurgeTiers)
}

// --- flat: one fee for every job ---

type flat struct {
	FeeCents int64 `yaml:"fee_cents"`
}

func newFlat(p strategy.Params) (Policy, error) {
	f := &flat{}
	if err := p.Decode(f); err != nil {
		return nil, err
	}
	if f.FeeCents < 0 {
		return nil, fmt.Errorf("fee_cents must be >= 0, got %d", f.FeeCents)
	}
	return f, nil
}

func (f *flat) Price(_ context.Context, in Input) (Quote, error) {
	return quote(in, f.FeeCents, 100), nil
}

// --- per_km: base + rate per started km, with an optional minimum ---

type perKm struct {
	BaseCents  int64 `yaml:"base_cents"`
	PerKmCents int64 `yaml:"per_km_cents"`
	MinCents   int64 `yaml:"min_cents"`
}

func newPerKm(p strategy.Params) (Policy, error) {
	f := &perKm{}
	if err := p.Decode(f); err != nil {
		return nil, err
	}
	if f.PerKmCents <= 0 {
		return nil, fmt.Errorf("per_km_cents must be > 0, got %d", f.PerKmCents)
	}
	return f, nil
}

// Price charges each started kilometre: 2.1 km is 3 km. Courier customers
// expect whole-km fares, and it never undercharges a leg.
func (f *perKm) Price(_ context.Context, in Input) (Quote, error) {
	km := int64(math.Ceil(DistanceMeters(in.Stops) / 1000))
	return quote(in, max(f.BaseCents+km*f.PerKmCents, f.MinCents), 100), nil
}

// --- per_kg: base + rate per started kg of packages ---

type perKg struct {
	BaseCents  int64 `yaml:"base_cents"`
	PerKgCents int64 `yaml:"per_kg_cents"`
	MinCents   int64 `yaml:"min_cents"`
}

func newPerKg(p strategy.Params) (Policy, error) {
	f := &perKg{}
	if err := p.Decode(f); err != nil {
		return nil, err
	}
	if f.PerKgCents <= 0 {
		return nil, fmt.Errorf("per_kg_cents must be > 0, got %d", f.PerKgCents)
	}
	return f, nil
}

func (f *perKg) Price(_ context.Context, in Input) (Quote, error) {
	if in.WeightGrams <= 0 {
		return Quote{}, fmt.Errorf("%w: per_kg pricing needs the packages' weight", ErrNoFare)
	}
	kg := (in.WeightGrams + 999) / 1000
	return quote(in, max(f.BaseCents+kg*f.PerKgCents, f.MinCents), 100), nil
}

// --- zone_table: a fare per (pickup zone, dropoff zone) ---

// Zones are circles: a centre and a radius. Real polygons would be more
// exact, but circles are what an operator can write down by hand from a
// map, and a buyer who needs polygons registers their own policy.
type zone struct {
	Name     string  `yaml:"name"`
	Lat      float64 `yaml:"lat"`
	Lng      float64 `yaml:"lng"`
	RadiusKm float64 `yaml:"radius_km"`
}

type fare struct {
	From     string `yaml:"from"`
	To       string `yaml:"to"`
	FeeCents int64  `yaml:"fee_cents"`
}

type zoneTable struct {
	Zones []zone `yaml:"zones"`
	Fares []fare `yaml:"fares"`
	// DefaultFeeCents prices a route no fare covers. Unset means such a
	// job is refused rather than priced by a guess.
	DefaultFeeCents *int64 `yaml:"default_fee_cents"`
}

func newZoneTable(p strategy.Params) (Policy, error) {
	z := &zoneTable{}
	if err := p.Decode(z); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, zn := range z.Zones {
		if zn.Name == "" || zn.RadiusKm <= 0 {
			return nil, fmt.Errorf("every zone needs a name and a radius_km > 0 (got %+v)", zn)
		}
		names[zn.Name] = true
	}
	for _, f := range z.Fares {
		if !names[f.From] || !names[f.To] {
			return nil, fmt.Errorf("fare %s -> %s names a zone that isn't declared", f.From, f.To)
		}
	}
	if len(z.Fares) == 0 && z.DefaultFeeCents == nil {
		return nil, errors.New("needs fares, a default_fee_cents, or both")
	}
	return z, nil
}

// zoneOf is the first declared zone containing the point, so overlapping
// zones resolve in the order the operator listed them.
func (z *zoneTable) zoneOf(l events.Location) string {
	for _, zn := range z.Zones {
		if geo.HaversineMeters(l.Lat, l.Lng, zn.Lat, zn.Lng) <= zn.RadiusKm*1000 {
			return zn.Name
		}
	}
	return ""
}

func (z *zoneTable) Price(_ context.Context, in Input) (Quote, error) {
	if len(in.Stops) < 2 {
		return Quote{}, fmt.Errorf("%w: zone pricing needs a pickup and a dropoff", ErrNoFare)
	}
	from, to := z.zoneOf(in.Stops[0].Location), z.zoneOf(in.Stops[len(in.Stops)-1].Location)
	for _, f := range z.Fares {
		if from != "" && f.From == from && f.To == to {
			return quote(in, f.FeeCents, 100), nil
		}
	}
	if z.DefaultFeeCents != nil {
		return quote(in, *z.DefaultFeeCents, 100), nil
	}
	return Quote{}, fmt.Errorf("%w: no fare from %q to %q", ErrNoFare, from, to)
}

// --- surge_tiers: a base fee times a multiplier from current load ---

type tier struct {
	UpToRatio     *float64 `yaml:"up_to_ratio"` // nil on the last tier: everything above
	MultiplierPct int      `yaml:"multiplier_pct"`
}

type surgeTiers struct {
	BaseFeeCents int64  `yaml:"base_fee_cents"`
	Tiers        []tier `yaml:"tiers"`
}

func newSurgeTiers(p strategy.Params) (Policy, error) {
	s := &surgeTiers{}
	if err := p.Decode(s); err != nil {
		return nil, err
	}
	if len(s.Tiers) == 0 {
		return nil, errors.New("needs at least one tier")
	}
	prev := math.Inf(-1)
	for i, t := range s.Tiers {
		if t.MultiplierPct < 100 {
			return nil, fmt.Errorf("tier %d: multiplier_pct %d is below 100: surge never discounts", i+1, t.MultiplierPct)
		}
		last := i == len(s.Tiers)-1
		switch {
		case last && t.UpToRatio != nil:
			return nil, errors.New("the last tier must have no up_to_ratio: it catches everything above the one before")
		case !last && t.UpToRatio == nil:
			return nil, fmt.Errorf("tier %d needs an up_to_ratio (only the last tier may leave it out)", i+1)
		case !last && *t.UpToRatio <= prev:
			return nil, fmt.Errorf("tier %d: up_to_ratio must increase", i+1)
		}
		if t.UpToRatio != nil {
			prev = *t.UpToRatio
		}
	}
	return s, nil
}

// Price multiplies the base fee by the tier for open jobs per free agent.
// No free agents counts as one, the worst realistic case, rather than a
// division by zero.
func (s *surgeTiers) Price(_ context.Context, in Input) (Quote, error) {
	free := max(in.Load.FreeAgents, 1)
	ratio := float64(in.Load.OpenJobs) / float64(free)
	pct := s.Tiers[len(s.Tiers)-1].MultiplierPct
	for _, t := range s.Tiers {
		if t.UpToRatio != nil && ratio <= *t.UpToRatio {
			pct = t.MultiplierPct
			break
		}
	}
	return quote(in, pctOf(s.BaseFeeCents, pct), pct), nil
}
