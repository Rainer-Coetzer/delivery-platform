package app

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// Every pack we ship must boot: its strategies exist and their params fit.
func TestShippedPacksBuildTheirStrategies(t *testing.T) {
	names := packs.List(packfs.FS)
	if len(names) == 0 {
		t.Fatal("no packs found")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PACK", name)
			p, err := LoadPack()
			if err != nil {
				t.Fatal(err)
			}
			s, err := BuildStrategies(p)
			if err != nil {
				t.Fatal(err)
			}
			if s.Pricing == nil || s.Dispatch == nil || s.ETA == nil || len(s.Payments.Methods()) == 0 {
				t.Fatalf("incomplete strategies: %+v", s)
			}
		})
	}
}

func TestBuildStrategiesNamesTheProblem(t *testing.T) {
	t.Setenv("PACK", "food")
	base, err := LoadPack()
	if err != nil {
		t.Fatal(err)
	}
	params := func(src string) yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(src), &n); err != nil {
			t.Fatal(err)
		}
		return *n.Content[0]
	}
	cases := []struct {
		name   string
		mutate func(s *packs.Strategies)
		want   string
	}{
		{"missing pricing", func(s *packs.Strategies) { s.Pricing = packs.Strategy{} }, "strategies.pricing is required"},
		{"unknown dispatch", func(s *packs.Strategies) { s.Dispatch = packs.Strategy{Name: "teleport"} }, `unknown dispatch strategy "teleport"`},
		{"param typo", func(s *packs.Strategies) { s.ETA.Params = params("{default_mph: 30}") }, "pack food: eta vehicle_speed: params"},
		{"provider twice", func(s *packs.Strategies) { s.Payment = append(s.Payment, s.Payment[0]) }, "payment provider simulated is listed twice"},
		{"no payment", func(s *packs.Strategies) { s.Payment = nil }, "at least one payment provider"},
		{"unknown channel", func(s *packs.Strategies) { s.Notify = []packs.Strategy{{Name: "pigeon"}} }, `unknown notify strategy "pigeon"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := *base
			p.Strategies.Payment = append([]packs.Strategy(nil), base.Strategies.Payment...)
			c.mutate(&p.Strategies)
			_, err := BuildStrategies(&p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}
