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

// The app ships English and Afrikaans (apps/delivery_app/lib/l10n), so
// every shipped pack says each of its words in both: otherwise an
// Afrikaans screen shows an English noun in the middle of a sentence.
// A buyer's own pack may translate less; words it leaves out fall back
// to English.
func TestShippedPacksSpeakEveryAppLocale(t *testing.T) {
	for _, n := range List(packfs.FS) {
		p, err := Load(packfs.FS, n, known)
		if err != nil {
			t.Fatal(err)
		}
		for key := range p.Terminology["en"] {
			if _, ok := p.Terminology["af"][key]; !ok {
				t.Errorf("pack %s: %q has no af word", n, key)
			}
		}
		for key := range p.Terminology["af"] {
			if _, ok := p.Terminology["en"][key]; !ok {
				t.Errorf("pack %s: af %q has no en word (a typo?)", n, key)
			}
		}
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
settings:
  packages: { max_lines: 10, max_quantity: 5, max_weight_g: 30000, max_dimension_mm: 1500, max_declared_value_cents: 1000000 }
  proof: { pin_attempts: 5, max_photo_bytes: 5000000 }
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
		{"proof on a stop kind that doesn't exist", strings.Replace(goodPack, "dropoff: photo", "doorstep: photo", 1), `"doorstep" is not a stop kind`},
		{"module without its settings", strings.Replace(goodPack, "  proof: { pin_attempts: 5, max_photo_bytes: 5000000 }\n", "", 1), "the proof module needs settings.proof"},
		{"settings without the module", goodPack + "  multi-stop: { max_stops: 5 }\n", "settings.multi-stop is set but the multi-stop module isn't enabled"},
		{"customer route with catalogue lines", strings.Replace(strings.Replace(goodPack, "lines: packages", "lines: catalogue", 1), "[packages, proof]", "[catalogue, packages, proof]", 1), "point_to_point needs lines: packages"},
		{"requires without capacity", strings.Replace(goodPack, "  proof: { dropoff: photo }\n", "  proof: { dropoff: photo }\n  requires: [cold_chain]\n", 1), "capacity module isn't enabled"},
		{"a zero limit", strings.Replace(goodPack, "max_weight_g: 30000", "max_weight_g: 0", 1), "settings.packages.max_weight_g must be > 0"},
		{"settings typo", strings.Replace(goodPack, "pin_attempts", "pin_attempt", 1), "field pin_attempt not found"},
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

func TestApprovalModuleNeedsARoleSetFact(t *testing.T) {
	withFact := strings.Replace(goodLifecycle, "transitions:", "facts:\n  approved: { by: [admin] }\ntransitions:", 1)
	cases := []struct{ name, pack, lifecycle, want string }{
		{"role-set fact without the module", goodPack, withFact, "needs the approval module"},
		{"module without a role-set fact", strings.Replace(goodPack, "[packages, proof]", "[packages, proof, approval]", 1), goodLifecycle, "approval module is enabled but"},
		{"both", strings.Replace(goodPack, "[packages, proof]", "[packages, proof, approval]", 1), withFact, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{"test/pack.yaml": {Data: []byte(c.pack)}, "test/lifecycle.yaml": {Data: []byte(c.lifecycle)}}
			_, err := Load(fsys, "test", known)
			if c.want == "" && err != nil {
				t.Fatal(err)
			}
			if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}
