package packs

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
)

var known = lifecycle.Known{
	Events: []string{"payments.completed", "payments.failed", "merchant.job-accepted", "merchant.job-rejected", "merchant.job-ready", "dispatch.stop-completed"},
	Roles:  []string{"customer", "merchant", "agent", "admin"},
}

// Every shipped pack must load and validate. This is the test that stops a
// broken pack from reaching a buyer.
func TestShippedPacksLoad(t *testing.T) {
	names := List(packfs.FS)
	if len(names) == 0 {
		t.Fatal("no packs embedded")
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			p, err := Load(packfs.FS, n, known)
			if err != nil {
				t.Fatal(err)
			}
			if p.Lifecycle == nil || p.Term("en", "job") == "" {
				t.Fatalf("pack %s loaded incompletely", n)
			}
		})
	}
}

func TestFoodTerminology(t *testing.T) {
	p, err := Load(packfs.FS, "food", known)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Term("en", "merchant"); got != "restaurant" {
		t.Fatalf("food merchant term = %q", got)
	}
	if got := p.Term("de", "agent"); got != "driver" {
		t.Fatalf("missing locale must fall back to en, got %q", got)
	}
	if got := p.Term("en", "no.such.key"); got != "no.such.key" {
		t.Fatalf("missing key must fall back to the key, got %q", got)
	}
}

const goodLifecycle = `schema: 1
initial: NEW
states:
  - { name: NEW, milestone: created }
  - { name: READY, milestone: ready_for_dispatch }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
transitions:
  - { from: NEW, to: READY, on: payments.completed }
  - { from: READY, to: DONE, on: dispatch.stop-completed, stop: last_stop }
  - { from: "*", to: GONE, by: [admin] }
`

const goodPack = `schema: 1
name: test
modules: [packages, proof]
roles: [customer, agent, admin]
job:
  stops: point_to_point
  lines: packages
  proof: { dropoff: photo }
strategies:
  pricing: { name: per_km }
  dispatch: { name: nearest }
  eta: { name: vehicle_speed }
  payment: [ { name: cash } ]
terminology:
  en: { job: delivery }
`

func TestLoad_Invalid(t *testing.T) {
	cases := []struct {
		name, pack, want string
	}{
		{"valid", goodPack, ""},
		{"typo key", strings.Replace(goodPack, "modules:", "moduels:", 1), "field moduels not found"},
		{"wrong schema", strings.Replace(goodPack, "schema: 1", "schema: 9", 1), "schema 9 is not supported"},
		{"name must match dir", strings.Replace(goodPack, "name: test", "name: other", 1), "must match its directory"},
		{"unknown module", strings.Replace(goodPack, "[packages, proof]", "[packages, proof, teleport]", 1), `unknown module "teleport"`},
		{"lines need their module", strings.Replace(goodPack, "[packages, proof]", "[proof]", 1), "packages needs the packages module"},
		{"proof needs module", strings.Replace(goodPack, "[packages, proof]", "[packages]", 1), "proof module isn't enabled"},
		{"bad proof kind", strings.Replace(goodPack, "dropoff: photo", "dropoff: selfie", 1), `"selfie"`},
		{"multi_stop needs module", strings.Replace(goodPack, "stops: point_to_point", "stops: multi_stop", 1), "multi_stop needs the multi-stop module"},
		{"unknown role", strings.Replace(goodPack, "[customer, agent, admin]", "[customer, pilot]", 1), `unknown role "pilot"`},
		{"strategies required", strings.Replace(goodPack, "  dispatch: { name: nearest }\n", "", 1), "strategies: pricing, dispatch, eta"},
		{"en required", strings.Replace(goodPack, "en: { job: delivery }", "af: { job: aflewering }", 1), "an en locale is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"test/pack.yaml":      {Data: []byte(c.pack)},
				"test/lifecycle.yaml": {Data: []byte(goodLifecycle)},
			}
			_, err := Load(fsys, "test", known)
			if c.want == "" {
				if err != nil {
					t.Fatalf("valid pack rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
	if _, err := Load(fstest.MapFS{}, "../etc", known); err == nil {
		t.Fatal("a path-like pack name must be rejected")
	}
	if _, err := Load(fstest.MapFS{}, "missing", known); err == nil || !strings.Contains(err.Error(), "available:") {
		t.Fatalf("a missing pack should list what's available, got %v", err)
	}
}
