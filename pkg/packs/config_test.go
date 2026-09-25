package packs

import (
	"encoding/json"
	"strings"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
)

func TestConfig(t *testing.T) {
	food, err := Load(packfs.FS, "food", known)
	if err != nil {
		t.Fatal(err)
	}
	c := food.Config("en", []string{"cash", "card"})
	if c.Pack.Name != "food" || c.Locale != "en" || strings.Join(c.PaymentMethods, ",") != "card,cash" {
		t.Fatalf("header: %+v", c)
	}
	if c.Terms["merchant"] != "restaurant" || c.Terms["agent"] != "driver" {
		t.Fatalf("food's words: %v", c.Terms)
	}
	// States in lifecycle order, labelled from terminology.
	if c.Lifecycle.Initial != "CREATED" || c.Lifecycle.States[0].Label != "Placed" || c.Lifecycle.States[0].Milestone != "created" {
		t.Fatalf("lifecycle: %+v", c.Lifecycle)
	}
	if c.Limits.Packages != nil || c.Limits.Proof != nil {
		t.Fatalf("food has no packages or proof limits: %+v", c.Limits)
	}
	if got := food.Config("xx", nil).Locale; got != "en" {
		t.Fatalf("an unknown locale falls back to en, got %s", got)
	}

	freight, err := Load(packfs.FS, "freight", known)
	if err != nil {
		t.Fatal(err)
	}
	fc := freight.Config("en", []string{"cash"})
	if len(fc.Lifecycle.Approvals) != 1 || fc.Lifecycle.Approvals[0].Fact != "approved" || fc.Lifecycle.Approvals[0].By[0] != "admin" {
		t.Fatalf("freight's approval: %+v", fc.Lifecycle.Approvals)
	}
	if fc.Limits.MultiStop == nil || fc.Limits.MultiStop.MaxStops != 8 || fc.Limits.Documents == nil || fc.Limits.Proof == nil {
		t.Fatalf("freight's limits: %+v", fc.Limits)
	}
	raw, _ := json.Marshal(fc)
	for _, leak := range []string{"pinAttempts", "pin_attempts"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("%s must not be public", leak)
		}
	}
	if !strings.Contains(string(raw), `"maxStops":8`) {
		t.Fatalf("limits serialise in camelCase: %s", raw)
	}
}

func TestLabelFallsBackToAReadableName(t *testing.T) {
	p := &Pack{Terminology: map[string]map[string]string{"en": {}}}
	if got := p.label("en", "state.IN_TRANSIT", "IN_TRANSIT"); got != "In transit" {
		t.Fatalf("got %q", got)
	}
}
