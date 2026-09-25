// Package lifecycle is a pack's job state machine (ADR-0003): states with
// pack-chosen names, mapped onto a fixed spine of milestones the core reacts
// to, plus facts (flags set by events or people) and transitions that fire
// on an event, on a person's action, or automatically once facts hold.
//
// A lifecycle is loaded from YAML at boot and validated there; anything
// wrong is a named error, so a broken pack never starts. After that,
// Machine.Apply is a pure function: state + trigger -> steps.
package lifecycle

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Milestones are the fixed semantic spine. Core modules react to these,
// never to a pack's state names. Adding one is a core change (ADR-0004).
const (
	Created          = "created"
	Confirmed        = "confirmed"
	ReadyForDispatch = "ready_for_dispatch"
	InTransit        = "in_transit"
	Completed        = "completed"
	Cancelled        = "cancelled"
)

var allMilestones = []string{Created, Confirmed, ReadyForDispatch, InTransit, Completed, Cancelled}

// Stop filters narrow an event trigger to a particular stop.
const (
	StopAny         = ""             // any stop (or not a stop event)
	StopFirstPickup = "first_pickup" // the job's first pickup stop
	StopLastStop    = "last_stop"    // the job's final stop
)

// SchemaVersion is the lifecycle YAML format this code reads.
const SchemaVersion = 1

// --- YAML shape ---

type spec struct {
	Schema      int                 `yaml:"schema"`
	Initial     string              `yaml:"initial"`
	States      []stateSpec         `yaml:"states"`
	Facts       map[string]factSpec `yaml:"facts"`
	Transitions []transitionSpec    `yaml:"transitions"`
}

type stateSpec struct {
	Name      string `yaml:"name"`
	Milestone string `yaml:"milestone"`
	Terminal  bool   `yaml:"terminal"`
}

// factSpec says what sets a fact: an event, or people in the given roles.
type factSpec struct {
	On string   `yaml:"on"`
	By []string `yaml:"by"`
}

type transitionSpec struct {
	From fromList `yaml:"from"`
	To   string   `yaml:"to"`
	// Exactly one of On, By, Auto.
	On   string   `yaml:"on"`
	Stop string   `yaml:"stop"`
	By   []string `yaml:"by"`
	Auto bool     `yaml:"auto"`
	// When is a guard: every listed fact must hold.
	When []string `yaml:"when"`
}

// fromList accepts `from: A`, `from: [A, B]`, or `from: "*"` (every
// non-terminal state).
type fromList []string

func (f *fromList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*f = fromList{n.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		*f = list
		return nil
	}
	return fmt.Errorf("line %d: from must be a state name or a list of state names", n.Line)
}

// --- Machine ---

// Known lists what a lifecycle may refer to. Anything else is a boot error.
type Known struct {
	Events []string // topics that can trigger transitions or set facts
	Roles  []string
}

type State struct {
	Name      string
	Milestone string // "" if the state maps to no milestone
	Terminal  bool
}

type Transition struct {
	From, To string
	On       string
	Stop     string
	By       []string
	Auto     bool
	When     []string
}

type Fact struct {
	Name string
	On   string
	By   []string
}

type Machine struct {
	Initial     string
	States      []State // in YAML order: the order a UI draws them in
	Facts       []Fact
	Transitions []Transition
	byName      map[string]State
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid lifecycle")

// Load parses and validates a lifecycle. required lists milestones the
// enabled modules depend on (e.g. dispatch needs ready_for_dispatch).
func Load(data []byte, known Known, required ...string) (*Machine, error) {
	var s spec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // a typo'd key is an error, not a silent default
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrInvalid, err)
	}
	if s.Schema != SchemaVersion {
		return nil, fmt.Errorf("%w: schema %d is not supported (this build reads schema %d)", ErrInvalid, s.Schema, SchemaVersion)
	}
	m, err := build(s)
	if err != nil {
		return nil, err
	}
	if err := m.validate(known, required); err != nil {
		return nil, err
	}
	return m, nil
}

func build(s spec) (*Machine, error) {
	m := &Machine{Initial: s.Initial, byName: map[string]State{}}
	for _, st := range s.States {
		if st.Name == "" {
			return nil, fmt.Errorf("%w: a state has no name", ErrInvalid)
		}
		if _, dup := m.byName[st.Name]; dup {
			return nil, fmt.Errorf("%w: state %q is declared twice", ErrInvalid, st.Name)
		}
		state := State{Name: st.Name, Milestone: st.Milestone, Terminal: st.Terminal}
		m.States = append(m.States, state)
		m.byName[st.Name] = state
	}
	names := make([]string, 0, len(s.Facts))
	for name := range s.Facts {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order for errors and /config
	for _, name := range names {
		f := s.Facts[name]
		m.Facts = append(m.Facts, Fact{Name: name, On: f.On, By: f.By})
	}
	for i, t := range s.Transitions {
		from := []string(t.From)
		if len(from) == 1 && from[0] == "*" {
			from = nil
			for _, st := range m.States {
				if !st.Terminal && st.Name != t.To {
					from = append(from, st.Name)
				}
			}
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("%w: transition %d (to %q) has no from", ErrInvalid, i+1, t.To)
		}
		for _, f := range from {
			m.Transitions = append(m.Transitions, Transition{From: f, To: t.To, On: t.On, Stop: t.Stop, By: t.By, Auto: t.Auto, When: t.When})
		}
	}
	return m, nil
}

func (m *Machine) validate(known Known, required []string) error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if st, ok := m.byName[m.Initial]; !ok {
		add("initial state %q is not declared", m.Initial)
	} else if st.Milestone != Created {
		// A job's milestone is the last one it reached, so it has to start
		// on one: every consumer reads jobs.created as "created".
		add("initial state %s must map milestone created", m.Initial)
	}
	milestoneOwner := map[string]string{}
	for _, st := range m.States {
		if st.Milestone == "" {
			continue
		}
		if !slices.Contains(allMilestones, st.Milestone) {
			add("state %s: unknown milestone %q (valid: %s)", st.Name, st.Milestone, strings.Join(allMilestones, ", "))
			continue
		}
		if prev, dup := milestoneOwner[st.Milestone]; dup {
			add("milestone %s is mapped by both %s and %s", st.Milestone, prev, st.Name)
		}
		milestoneOwner[st.Milestone] = st.Name
	}
	for _, ms := range append([]string{Completed, Cancelled}, required...) {
		if _, ok := milestoneOwner[ms]; !ok {
			add("no state maps milestone %s (required)", ms)
		}
	}
	if st, ok := m.byName[milestoneOwner[Completed]]; ok && !st.Terminal {
		add("state %s maps completed but isn't terminal", st.Name)
	}
	if st, ok := m.byName[milestoneOwner[Cancelled]]; ok && !st.Terminal {
		add("state %s maps cancelled but isn't terminal", st.Name)
	}

	facts := map[string]bool{}
	for _, f := range m.Facts {
		facts[f.Name] = true
		if (f.On == "") == (len(f.By) == 0) {
			add("fact %s: set exactly one of on (an event) or by (roles)", f.Name)
		}
		if f.On != "" && !slices.Contains(known.Events, f.On) {
			add("fact %s: unknown event %q", f.Name, f.On)
		}
		for _, r := range f.By {
			if !slices.Contains(known.Roles, r) {
				add("fact %s: unknown role %q", f.Name, r)
			}
		}
	}

	type key struct{ from, trigger string }
	seen := map[key]bool{}
	for i, t := range m.Transitions {
		where := fmt.Sprintf("transition %d (%s -> %s)", i+1, t.From, t.To)
		from, fok := m.byName[t.From]
		if !fok {
			add("%s: unknown from state", where)
		}
		if _, ok := m.byName[t.To]; !ok {
			add("%s: unknown to state", where)
		}
		if fok && from.Terminal {
			add("%s: terminal state %s can't have exits", where, t.From)
		}
		kinds := 0
		if t.On != "" {
			kinds++
		}
		if len(t.By) > 0 {
			kinds++
		}
		if t.Auto {
			kinds++
		}
		if kinds != 1 {
			add("%s: needs exactly one trigger (on, by or auto), has %d", where, kinds)
		}
		if t.On != "" && !slices.Contains(known.Events, t.On) {
			add("%s: unknown event %q", where, t.On)
		}
		if t.Stop != StopAny && t.Stop != StopFirstPickup && t.Stop != StopLastStop {
			add("%s: unknown stop filter %q (valid: %s, %s)", where, t.Stop, StopFirstPickup, StopLastStop)
		}
		if t.Stop != StopAny && t.On == "" {
			add("%s: a stop filter only applies to an event trigger", where)
		}
		for _, r := range t.By {
			if !slices.Contains(known.Roles, r) {
				add("%s: unknown role %q", where, r)
			}
		}
		for _, f := range t.When {
			if !facts[f] {
				add("%s: guard names undeclared fact %q", where, f)
			}
		}
		// Two transitions leaving the same state on the same trigger would
		// make the outcome depend on YAML order. Roles are exempt: a person
		// picks the target explicitly.
		trig := fmt.Sprintf("on=%s stop=%s auto=%v when=%v", t.On, t.Stop, t.Auto, t.When)
		if len(t.By) == 0 {
			if seen[key{t.From, trig}] {
				add("%s: another transition leaves %s on the same trigger", where, t.From)
			}
			seen[key{t.From, trig}] = true
		}
	}

	if len(problems) == 0 {
		problems = append(problems, m.graphProblems()...)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w:\n  - %s", ErrInvalid, strings.Join(problems, "\n  - "))
	}
	return nil
}

// graphProblems: every state reachable from initial, every non-terminal
// state able to reach a terminal one, and no cycle of automatic transitions
// (which would spin forever inside Apply).
func (m *Machine) graphProblems() []string {
	var problems []string
	out := map[string][]string{}
	autoOut := map[string][]string{}
	for _, t := range m.Transitions {
		out[t.From] = append(out[t.From], t.To)
		if t.Auto {
			autoOut[t.From] = append(autoOut[t.From], t.To)
		}
	}
	reach := map[string]bool{m.Initial: true}
	queue := []string{m.Initial}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, n := range out[s] {
			if !reach[n] {
				reach[n] = true
				queue = append(queue, n)
			}
		}
	}
	for _, st := range m.States {
		if !reach[st.Name] {
			problems = append(problems, fmt.Sprintf("state %s is unreachable from %s", st.Name, m.Initial))
		}
		if !st.Terminal && !m.canTerminate(st.Name, out) {
			problems = append(problems, fmt.Sprintf("state %s can never reach a terminal state", st.Name))
		}
	}
	for _, st := range m.States {
		if cycle := autoCycle(st.Name, autoOut, map[string]bool{}); cycle {
			problems = append(problems, fmt.Sprintf("automatic transitions form a cycle through %s", st.Name))
			break
		}
	}
	return problems
}

func (m *Machine) canTerminate(start string, out map[string][]string) bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if m.byName[s].Terminal {
			return true
		}
		for _, n := range out[s] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return false
}

func autoCycle(s string, autoOut map[string][]string, onPath map[string]bool) bool {
	if onPath[s] {
		return true
	}
	onPath[s] = true
	defer delete(onPath, s)
	for _, n := range autoOut[s] {
		if autoCycle(n, autoOut, onPath) {
			return true
		}
	}
	return false
}

// --- Running it ---

// Trigger is what happened to a job: an event (with stop context for
// dispatch events), or a person in a role asking to move it To a state.
type Trigger struct {
	Event string
	Stop  StopInfo
	Role  string
	To    string // role triggers name their target
}

// StopInfo describes the stop a dispatch event refers to.
type StopInfo struct {
	FirstPickup bool
	LastStop    bool
}

// Step is one transition taken.
type Step struct {
	From, To  string
	Milestone string // the To state's milestone, "" if none
}

var ErrNoTransition = errors.New("no transition for this trigger")

// Apply runs trigger against a job in state with facts. It returns the
// steps taken (the triggered one, then any automatic ones it unlocks) and
// the updated facts. An event that only sets a fact returns no steps and no
// error. A trigger that neither sets a fact nor matches a transition is
// ErrNoTransition, which callers treat as permanent: retrying won't help.
func (m *Machine) Apply(state string, facts map[string]bool, t Trigger) ([]Step, map[string]bool, error) {
	if _, ok := m.byName[state]; !ok {
		return nil, facts, fmt.Errorf("job is in state %q, which this lifecycle doesn't have", state)
	}
	next := make(map[string]bool, len(facts)+1)
	for k, v := range facts {
		next[k] = v
	}
	factSet := false
	if t.Event != "" {
		for _, f := range m.Facts {
			if f.On == t.Event && !next[f.Name] {
				next[f.Name] = true
				factSet = true
			}
		}
	}

	var steps []Step
	if tr, ok := m.match(state, next, t); ok {
		steps = append(steps, m.step(state, tr.To))
		state = tr.To
	} else if !factSet {
		return nil, facts, fmt.Errorf("%w: %s in state %s", ErrNoTransition, describe(t), state)
	}

	// Automatic transitions; validation guarantees they can't cycle, and a
	// path can't be longer than the number of states (Settle's bound).
	return append(steps, m.Settle(state, next)...), next, nil
}

// Settle takes the automatic transitions available from state, for a job
// that was just created (nothing triggered it, but its initial state may
// move on by itself).
func (m *Machine) Settle(state string, facts map[string]bool) []Step {
	var steps []Step
	for range m.States {
		tr, ok := m.autoFrom(state, facts)
		if !ok {
			break
		}
		steps = append(steps, m.step(state, tr.To))
		state = tr.To
	}
	return steps
}

// Events lists every event this lifecycle reacts to (transition triggers
// and fact sources), sorted: what the jobs module subscribes to.
func (m *Machine) Events() []string {
	seen := map[string]bool{}
	for _, t := range m.Transitions {
		if t.On != "" {
			seen[t.On] = true
		}
	}
	for _, f := range m.Facts {
		if f.On != "" {
			seen[f.On] = true
		}
	}
	out := make([]string, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// Reacts reports whether event can trigger a transition or set a fact.
func (m *Machine) Reacts(event string) bool {
	return slices.Contains(m.Events(), event)
}

// SetFact applies a role-set fact (e.g. an admin approving a job), then any
// automatic transitions that unlocks.
func (m *Machine) SetFact(state string, facts map[string]bool, fact, role string) ([]Step, map[string]bool, error) {
	var spec *Fact
	for i := range m.Facts {
		if m.Facts[i].Name == fact {
			spec = &m.Facts[i]
		}
	}
	if spec == nil {
		return nil, facts, fmt.Errorf("%w: unknown fact %q", ErrNoTransition, fact)
	}
	if !slices.Contains(spec.By, role) {
		return nil, facts, fmt.Errorf("%w: role %s may not set %s", ErrNoTransition, role, fact)
	}
	next := make(map[string]bool, len(facts)+1)
	for k, v := range facts {
		next[k] = v
	}
	next[fact] = true
	return m.Settle(state, next), next, nil
}

func (m *Machine) match(state string, facts map[string]bool, t Trigger) (Transition, bool) {
	for _, tr := range m.Transitions {
		if tr.From != state || !guards(tr.When, facts) {
			continue
		}
		switch {
		case t.Event != "" && tr.On == t.Event && stopMatches(tr.Stop, t.Stop):
			return tr, true
		case t.Role != "" && tr.To == t.To && slices.Contains(tr.By, t.Role):
			return tr, true
		}
	}
	return Transition{}, false
}

func (m *Machine) autoFrom(state string, facts map[string]bool) (Transition, bool) {
	for _, tr := range m.Transitions {
		if tr.From == state && tr.Auto && guards(tr.When, facts) {
			return tr, true
		}
	}
	return Transition{}, false
}

func (m *Machine) step(from, to string) Step {
	return Step{From: from, To: to, Milestone: m.byName[to].Milestone}
}

// State returns a declared state by name.
func (m *Machine) State(name string) (State, bool) {
	s, ok := m.byName[name]
	return s, ok
}

// StateFor returns the state that maps milestone, if any.
func (m *Machine) StateFor(milestone string) (State, bool) {
	for _, s := range m.States {
		if s.Milestone == milestone {
			return s, true
		}
	}
	return State{}, false
}

// RoleTargets lists the states role may move a job in state to.
func (m *Machine) RoleTargets(state, role string) []string {
	var out []string
	for _, tr := range m.Transitions {
		if tr.From == state && slices.Contains(tr.By, role) {
			out = append(out, tr.To)
		}
	}
	return out
}

func guards(when []string, facts map[string]bool) bool {
	for _, f := range when {
		if !facts[f] {
			return false
		}
	}
	return true
}

func stopMatches(filter string, s StopInfo) bool {
	switch filter {
	case StopFirstPickup:
		return s.FirstPickup
	case StopLastStop:
		return s.LastStop
	}
	return true
}

func describe(t Trigger) string {
	if t.Event != "" {
		return "event " + t.Event
	}
	return fmt.Sprintf("role %s -> %s", t.Role, t.To)
}
