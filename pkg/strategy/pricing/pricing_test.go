package pricing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

func build(t *testing.T, name, params string) (Policy, error) {
	t.Helper()
	var node yaml.Node
	if params != "" {
		if err := yaml.Unmarshal([]byte(params), &node); err != nil {
			t.Fatal(err)
		}
		node = *node.Content[0]
	}
	return Build(name, strategy.NewParams("pricing "+name, &node))
}

func mustBuild(t *testing.T, name, params string) Policy {
	t.Helper()
	p, err := build(t, name, params)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func price(t *testing.T, p Policy, in Input) Quote {
	t.Helper()
	q, err := p.Price(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalCents != q.SubtotalCents+q.FeeCents {
		t.Fatalf("total %d != subtotal %d + fee %d", q.TotalCents, q.SubtotalCents, q.FeeCents)
	}
	return q
}

// Windhoek CBD to Katutura is about 5.4 km as the crow flies.
var cbd = events.Location{Lat: -22.5700, Lng: 17.0836}
var katutura = events.Location{Lat: -22.5250, Lng: 17.0600}

func route(a, b events.Location) []events.Stop {
	return []events.Stop{{Seq: 0, Kind: events.StopPickup, Location: a}, {Seq: 1, Kind: events.StopDropoff, Location: b}}
}

// The food pack's numbers must give exactly the fees the hardcoded formula
// did (base 1000 x 1.0/1.2/1.5/2.0 at 5 assumed free agents).
func TestSurgeTiers_MatchesTheOriginalFoodFormula(t *testing.T) {
	p := mustBuild(t, "surge_tiers", `
base_fee_cents: 1000
tiers:
  - { up_to_ratio: 1, multiplier_pct: 100 }
  - { up_to_ratio: 2, multiplier_pct: 120 }
  - { up_to_ratio: 4, multiplier_pct: 150 }
  - { multiplier_pct: 200 }`)
	for _, c := range []struct {
		open, free int
		fee        int64
		pct        int
	}{
		{0, 5, 1000, 100}, {5, 5, 1000, 100}, {8, 5, 1200, 120}, {15, 5, 1500, 150}, {30, 5, 2000, 200},
		{10, 0, 2000, 200}, // no free agents counts as one
	} {
		q := price(t, p, Input{SubtotalCents: 6500, Load: Load{OpenJobs: c.open, FreeAgents: c.free}})
		if q.FeeCents != c.fee || q.MultiplierPct != c.pct || q.TotalCents != 6500+c.fee {
			t.Errorf("open=%d free=%d: got %+v, want fee %d at %d%%", c.open, c.free, q, c.fee, c.pct)
		}
	}
}

func TestPerKm_ChargesStartedKilometres(t *testing.T) {
	p := mustBuild(t, "per_km", `{base_cents: 2000, per_km_cents: 800, min_cents: 3000}`)
	if q := price(t, p, Input{Stops: route(cbd, katutura)}); q.FeeCents != 2000+6*800 {
		t.Fatalf("5.x km should bill 6 km: fee %d", q.FeeCents)
	}
	if q := price(t, p, Input{Stops: route(cbd, cbd)}); q.FeeCents != 3000 {
		t.Fatalf("the minimum applies to a zero-length job: fee %d", q.FeeCents)
	}
}

func TestPerKg(t *testing.T) {
	p := mustBuild(t, "per_kg", `{base_cents: 5000, per_kg_cents: 150}`)
	if q := price(t, p, Input{WeightGrams: 12001}); q.FeeCents != 5000+13*150 {
		t.Fatalf("12.001 kg bills 13 kg: fee %d", q.FeeCents)
	}
	if _, err := p.Price(context.Background(), Input{}); !errors.Is(err, ErrNoFare) {
		t.Fatalf("no weight: want ErrNoFare, got %v", err)
	}
}

func TestZoneTable(t *testing.T) {
	const zones = `
zones:
  - { name: cbd, lat: -22.5700, lng: 17.0836, radius_km: 2 }
  - { name: katutura, lat: -22.5250, lng: 17.0600, radius_km: 3 }
fares:
  - { from: cbd, to: katutura, fee_cents: 4500 }`
	p := mustBuild(t, "zone_table", zones)
	if q := price(t, p, Input{Stops: route(cbd, katutura)}); q.FeeCents != 4500 {
		t.Fatalf("cbd -> katutura: fee %d", q.FeeCents)
	}
	if _, err := p.Price(context.Background(), Input{Stops: route(katutura, cbd)}); !errors.Is(err, ErrNoFare) {
		t.Fatalf("a route with no fare and no default is refused, got %v", err)
	}
	withDefault := mustBuild(t, "zone_table", zones+"\ndefault_fee_cents: 9900")
	if q := price(t, withDefault, Input{Stops: route(katutura, cbd)}); q.FeeCents != 9900 {
		t.Fatalf("default fare: %d", q.FeeCents)
	}
}

func TestBootErrors(t *testing.T) {
	cases := []struct{ name, params, want string }{
		{"per_km", `{base_cents: 1, per_km_cent: 800}`, "field per_km_cent not found"}, // typo
		{"per_km", `{base_cents: 1}`, "per_km_cents must be > 0"},
		{"flat", `{fee_cents: -1}`, "fee_cents must be >= 0"},
		{"surge_tiers", `{base_fee_cents: 1000, tiers: [{up_to_ratio: 1, multiplier_pct: 90}, {multiplier_pct: 200}]}`, "never discounts"},
		{"surge_tiers", `{base_fee_cents: 1000, tiers: [{up_to_ratio: 2, multiplier_pct: 100}, {up_to_ratio: 1, multiplier_pct: 120}, {multiplier_pct: 200}]}`, "must increase"},
		{"surge_tiers", `{base_fee_cents: 1000, tiers: [{up_to_ratio: 1, multiplier_pct: 100}]}`, "last tier must have no up_to_ratio"},
		{"zone_table", `{zones: [{name: a, lat: 0, lng: 0, radius_km: 1}], fares: [{from: a, to: b, fee_cents: 1}]}`, "isn't declared"},
		{"teleport", ``, "unknown pricing strategy \"teleport\" (registered: flat, per_kg, per_km, surge_tiers, zone_table)"},
	}
	for _, c := range cases {
		_, err := build(t, c.name, c.params)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: want error containing %q, got %v", c.name, c.params, c.want, err)
		}
	}
}

func TestFlat(t *testing.T) {
	p := mustBuild(t, "flat", `{fee_cents: 2500}`)
	if q := price(t, p, Input{SubtotalCents: 100}); q.FeeCents != 2500 || q.TotalCents != 2600 || q.MultiplierPct != 100 {
		t.Fatalf("flat: %+v", q)
	}
	if got := strings.Join(Names(), ","); got != "flat,per_kg,per_km,surge_tiers,zone_table" {
		t.Fatalf("built-ins = %s", got)
	}
}

// Distance runs through every stop in order, not straight from first to
// last: a round trip is not zero kilometres.
func TestDistanceThroughStops(t *testing.T) {
	there := DistanceMeters(route(cbd, katutura))
	back := DistanceMeters([]events.Stop{{Location: cbd}, {Location: katutura}, {Location: cbd}})
	if there < 5000 || there > 6000 || back < 2*there-1 || back > 2*there+1 {
		t.Fatalf("there %.0f m, there and back %.0f m", there, back)
	}
}

func TestMoreBootErrors(t *testing.T) {
	cases := []struct{ name, params, want string }{
		{"per_kg", `{base_cents: 1}`, "per_kg_cents must be > 0"},
		{"zone_table", `{zones: [{name: a, lat: 0, lng: 0}]}`, "radius_km > 0"},
		{"zone_table", `{zones: [{name: a, lat: 0, lng: 0, radius_km: 1}]}`, "needs fares, a default_fee_cents, or both"},
		{"surge_tiers", `{base_fee_cents: 1000}`, "at least one tier"},
		{"surge_tiers", `{base_fee_cents: 1000, tiers: [{multiplier_pct: 100}, {multiplier_pct: 200}]}`, "tier 1 needs an up_to_ratio"},
	}
	for _, c := range cases {
		_, err := build(t, c.name, c.params)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: want error containing %q, got %v", c.name, c.params, c.want, err)
		}
	}
	z := mustBuild(t, "zone_table", `{zones: [{name: a, lat: 0, lng: 0, radius_km: 1}], default_fee_cents: 5}`)
	if _, err := z.Price(context.Background(), Input{}); !errors.Is(err, ErrNoFare) {
		t.Fatalf("zone pricing without stops: %v", err)
	}
}
