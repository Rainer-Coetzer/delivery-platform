package payment

import (
	"context"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
)

func build(t *testing.T, name, params string) Provider {
	t.Helper()
	p, err := Build(name, strategytest.Params(t, "payment "+name, params))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func authorize(t *testing.T, p Provider, amount int64) Result {
	t.Helper()
	r, err := p.Authorize(context.Background(), Charge{JobID: "j1", AmountCents: amount})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSimulated(t *testing.T) {
	always, never := build(t, "simulated", `{success_rate_pct: 100}`), build(t, "simulated", `{success_rate_pct: 0}`)
	for i := 0; i < 50; i++ {
		if !authorize(t, always, 100).Approved || authorize(t, never, 100).Approved {
			t.Fatal("0% and 100% must be deterministic")
		}
	}
	if r := authorize(t, always, 0); r.Approved || r.Reason != "invalid amount" {
		t.Fatalf("zero amount: %+v", r)
	}
	if m := always.Methods(); len(m) != 1 || m[0] != "card" {
		t.Fatalf("simulated handles card by default, got %v", m)
	}
}

func TestCash(t *testing.T) {
	c := build(t, "cash", "")
	if !authorize(t, c, 100).Approved || authorize(t, c, -1).Approved {
		t.Fatal("cash approves any positive amount")
	}
}

func TestSet(t *testing.T) {
	s, err := NewSet(map[string]Provider{"simulated": build(t, "simulated", ""), "cash": build(t, "cash", "")})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Methods(), ","); got != "card,cash" {
		t.Fatalf("methods = %s", got)
	}
	if _, ok := s.For("bitcoin"); ok {
		t.Fatal("an unlisted method has no provider")
	}
	if _, err := NewSet(map[string]Provider{"simulated": build(t, "simulated", `{methods: [card, cash]}`), "cash": build(t, "cash", "")}); err == nil ||
		!strings.Contains(err.Error(), `"cash" is claimed by both`) {
		t.Fatalf("two providers for one method: %v", err)
	}
	if _, err := NewSet(nil); err == nil {
		t.Fatal("no providers must be refused")
	}
}

func TestBootErrors(t *testing.T) {
	if _, err := Build("simulated", strategytest.Params(t, "payment", `{success_rate_pct: 101}`)); err == nil || !strings.Contains(err.Error(), "0-100") {
		t.Fatalf("got %v", err)
	}
}
