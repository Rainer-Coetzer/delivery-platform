// Package strategy is the registry every strategy kind is built on
// (ADR-0004, tier 2). A kind (pricing, dispatch, eta, payment, notify) has
// one Registry in pkg/strategy/<kind>. Built-in strategies and a buyer's
// own register the same way, from an init func:
//
//	func init() {
//	    pricing.Registry.Register("fuel_surcharge", func(p strategy.Params) (pricing.Policy, error) {
//	        var cfg struct{ PerKmCents int64 `yaml:"per_km_cents"` }
//	        if err := p.Decode(&cfg); err != nil {
//	            return nil, err
//	        }
//	        ...
//	    })
//	}
//
// A pack picks one by name (pack.yaml `strategies:`). Boot fails, naming the
// registered choices, if the name is unknown or the params don't fit.
package strategy

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// Params are a strategy's `params:` from pack.yaml, not yet decoded.
type Params struct {
	node *yaml.Node
	// Where they came from, for error messages: "pricing surge_tiers".
	where string
}

// NewParams wraps a params node. A nil or empty node decodes as no params.
func NewParams(where string, node *yaml.Node) Params { return Params{node: node, where: where} }

// Decode fills into (a pointer to the strategy's own config struct) from
// the params, strictly: a key the struct doesn't have is an error, so a
// typo in a rate can't be silently ignored.
func (p Params) Decode(into any) error {
	if p.node == nil || p.node.Kind == 0 {
		return nil
	}
	// yaml.Node.Decode has no strict mode; re-encode and decode with
	// KnownFields, which does.
	raw, err := yaml.Marshal(p.node)
	if err != nil {
		return fmt.Errorf("%s: params: %w", p.where, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%s: params: %w", p.where, err)
	}
	return nil
}

// Factory builds a strategy of kind T from its params.
type Factory[T any] func(Params) (T, error)

// Registry holds the strategies of one kind by name.
type Registry[T any] struct {
	kind      string
	mu        sync.RWMutex
	factories map[string]Factory[T]
}

func NewRegistry[T any](kind string) *Registry[T] {
	return &Registry[T]{kind: kind, factories: map[string]Factory[T]{}}
}

// Register adds a strategy. Registering a name twice panics: it happens at
// init, and two packages claiming one name is a build mistake to fix, not
// a runtime condition to handle.
func (r *Registry[T]) Register(name string, f Factory[T]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		panic(fmt.Sprintf("%s strategy %q registered twice", r.kind, name))
	}
	r.factories[name] = f
}

// Build makes the named strategy from its params.
func (r *Registry[T]) Build(name string, params Params) (T, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		var zero T
		return zero, fmt.Errorf("unknown %s strategy %q (registered: %s)", r.kind, name, strings.Join(r.Names(), ", "))
	}
	s, err := f(params)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s strategy %s: %w", r.kind, name, err)
	}
	return s, nil
}

// Names lists the registered strategies, sorted.
func (r *Registry[T]) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Has reports whether name is registered.
func (r *Registry[T]) Has(name string) bool { return slices.Contains(r.Names(), name) }
