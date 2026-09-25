package lifecycle

import (
	"errors"
	"os"
	"strings"
	"testing"
)

var known = Known{
	Events: []string{"payments.completed", "payments.failed", "merchant.job-accepted", "merchant.job-rejected", "merchant.job-ready", "dispatch.stop-completed"},
	Roles:  []string{"customer", "merchant", "agent", "admin"},
}

func loadFood(t *testing.T) *Machine {
	t.Helper()
	data, err := os.ReadFile("../../packs/food/lifecycle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(data, known, ReadyForDispatch)
	if err != nil {
		t.Fatalf("food lifecycle must load: %v", err)
	}
	return m
}

// minimal is a valid base the invalid cases mutate.
const minimal = `schema: 1
initial: NEW
states:
  - { name: NEW, milestone: created }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
transitions:
  - { from: NEW, to: DONE, on: payments.completed }
  - { from: NEW, to: GONE, by: [admin] }
`

func TestLoad_Invalid(t *testing.T) {
	cases := []struct {
		name, yaml, wantInError string
	}{
		{"unknown key (typo) is rejected", strings.Replace(minimal, "initial:", "inital:", 1), "field inital not found"},
		{"wrong schema version", strings.Replace(minimal, "schema: 1", "schema: 2", 1), "schema 2 is not supported"},
		{"unknown initial", strings.Replace(minimal, "initial: NEW", "initial: NOPE", 1), `initial state "NOPE"`},
		{"missing completed", strings.Replace(minimal, "milestone: completed, ", "", 1), "no state maps milestone completed"},
		{"required milestone missing", minimal, "no state maps milestone ready_for_dispatch"},
		{"unknown milestone", strings.Replace(minimal, "milestone: created", "milestone: begun", 1), `unknown milestone "begun"`},
		{"terminal with exits", minimal + "  - { from: DONE, to: GONE, by: [admin] }\n", "terminal state DONE can't have exits"},
		{"two triggers", strings.Replace(minimal, "on: payments.completed }", "on: payments.completed, by: [admin] }", 1), "needs exactly one trigger"},
		{"no trigger", strings.Replace(minimal, ", on: payments.completed }", " }", 1), "needs exactly one trigger"},
		{"unknown event", strings.Replace(minimal, "payments.completed", "payments.teleported", 1), `unknown event "payments.teleported"`},
		{"unknown role", strings.Replace(minimal, "by: [admin]", "by: [wizard]", 1), `unknown role "wizard"`},
		{"guard on undeclared fact", strings.Replace(minimal, "on: payments.completed }", "on: payments.completed, when: [paid] }", 1), `undeclared fact "paid"`},
		{"unreachable state", strings.Replace(minimal, "states:\n", "states:\n  - { name: LIMBO }\n", 1) + "  - { from: LIMBO, to: DONE, by: [admin] }\n", "LIMBO is unreachable"},
		{"dead end", strings.Replace(minimal, "states:\n", "states:\n  - { name: STUCK }\n", 1) + "  - { from: NEW, to: STUCK, by: [admin] }\n", "STUCK can never reach a terminal state"},
		{"ambiguous event", minimal + "  - { from: NEW, to: GONE, on: payments.completed }\n", "same trigger"},
		{"auto cycle", `schema: 1
initial: A
states:
  - { name: A, milestone: created }
  - { name: B }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
transitions:
  - { from: A, to: B, auto: true }
  - { from: B, to: A, auto: true }
  - { from: A, to: DONE, by: [admin] }
  - { from: A, to: GONE, by: [admin] }
`, "cycle"},
		{"completed not terminal", strings.Replace(minimal, "milestone: completed, terminal: true", "milestone: completed", 1) + "  - { from: DONE, to: GONE, by: [admin] }\n", "maps completed but isn't terminal"},
		{"milestone mapped twice", strings.Replace(minimal, "{ name: GONE, milestone: cancelled", "{ name: GONE, milestone: completed", 1), "mapped by both"},
		{"stop filter without event", strings.Replace(minimal, "by: [admin] }", "by: [admin], stop: last_stop }", 1), "stop filter only applies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			required := []string{}
			if c.name == "required milestone missing" {
				required = append(required, ReadyForDispatch)
			}
			_, err := Load([]byte(c.yaml), known, required...)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), c.wantInError) {
				t.Fatalf("error should mention %q, got:\n%v", c.wantInError, err)
			}
		})
	}
	if _, err := Load([]byte(minimal), known); err != nil {
		t.Fatalf("the base case must be valid: %v", err)
	}
}

func TestFood_HappyPath(t *testing.T) {
	m := loadFood(t)
	state, facts := m.Initial, map[string]bool{}

	apply := func(tr Trigger, wantState string) []Step {
		t.Helper()
		steps, f, err := m.Apply(state, facts, tr)
		if err != nil {
			t.Fatalf("%+v in %s: %v", tr, state, err)
		}
		facts = f
		if len(steps) > 0 {
			state = steps[len(steps)-1].To
		}
		if state != wantState {
			t.Fatalf("after %+v: state %s, want %s", tr, state, wantState)
		}
		return steps
	}

	// Payment first: only a fact, no transition yet.
	if steps := apply(Trigger{Event: "payments.completed"}, "CREATED"); len(steps) != 0 {
		t.Fatalf("paid alone must not move the job, got %v", steps)
	}
	// Acceptance completes the join: CONFIRMED then straight to PREPARING.
	steps := apply(Trigger{Event: "merchant.job-accepted"}, "PREPARING")
	if len(steps) != 2 || steps[0].To != "CONFIRMED" || steps[0].Milestone != Confirmed || steps[1].To != "PREPARING" {
		t.Fatalf("join should step CREATED->CONFIRMED->PREPARING, got %+v", steps)
	}
	apply(Trigger{Event: "merchant.job-ready"}, "READY")
	// A stop completion that isn't the first pickup doesn't move READY.
	if _, _, err := m.Apply(state, facts, Trigger{Event: "dispatch.stop-completed", Stop: StopInfo{LastStop: true}}); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("dropoff before pickup must be refused, got %v", err)
	}
	apply(Trigger{Event: "dispatch.stop-completed", Stop: StopInfo{FirstPickup: true}}, "OUT_FOR_DELIVERY")
	steps = apply(Trigger{Event: "dispatch.stop-completed", Stop: StopInfo{LastStop: true}}, "DELIVERED")
	if steps[0].Milestone != Completed {
		t.Fatalf("DELIVERED should map completed, got %q", steps[0].Milestone)
	}
	// Terminal: nothing moves it.
	if _, _, err := m.Apply(state, facts, Trigger{Role: "admin", To: "CANCELLED"}); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("terminal state must not accept a cancel, got %v", err)
	}
}

func TestFood_AcceptedFirstThenPaid(t *testing.T) {
	m := loadFood(t)
	steps, facts, err := m.Apply("CREATED", nil, Trigger{Event: "merchant.job-accepted"})
	if err != nil || len(steps) != 0 {
		t.Fatalf("accepted alone: steps=%v err=%v", steps, err)
	}
	steps, _, err = m.Apply("CREATED", facts, Trigger{Event: "payments.completed"})
	if err != nil || len(steps) != 2 || steps[1].To != "PREPARING" {
		t.Fatalf("paid second should complete the join: %+v %v", steps, err)
	}
}

func TestFood_RoleCancels(t *testing.T) {
	m := loadFood(t)
	cases := []struct {
		state, role string
		ok          bool
	}{
		{"CREATED", "customer", true},
		{"CONFIRMED", "customer", true},
		{"PREPARING", "customer", false}, // prep started
		{"OUT_FOR_DELIVERY", "admin", true},
		{"CREATED", "agent", false},
	}
	for _, c := range cases {
		_, _, err := m.Apply(c.state, nil, Trigger{Role: c.role, To: "CANCELLED"})
		if (err == nil) != c.ok {
			t.Errorf("%s cancels from %s: err=%v, want ok=%v", c.role, c.state, err, c.ok)
		}
	}
	if got := m.RoleTargets("CREATED", "customer"); len(got) != 1 || got[0] != "CANCELLED" {
		t.Errorf("RoleTargets(CREATED, customer) = %v", got)
	}
}

func TestSetFact(t *testing.T) {
	m, err := Load([]byte(`schema: 1
initial: NEW
states:
  - { name: NEW, milestone: created }
  - { name: APPROVED, milestone: ready_for_dispatch }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
facts:
  approved: { by: [admin, merchant] }
transitions:
  - { from: NEW, to: APPROVED, auto: true, when: [approved] }
  - { from: APPROVED, to: DONE, on: payments.completed }
  - { from: "*", to: GONE, by: [admin] }
`), known)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.SetFact("NEW", nil, "approved", "customer"); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("a customer may not approve: %v", err)
	}
	steps, facts, err := m.SetFact("NEW", nil, "approved", "merchant")
	if err != nil || len(steps) != 1 || steps[0].To != "APPROVED" || !facts["approved"] {
		t.Fatalf("merchant approval should unlock the auto step: %+v %v %v", steps, facts, err)
	}
}
