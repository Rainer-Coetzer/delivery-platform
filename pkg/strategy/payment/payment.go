// Package payment is the payment provider kind: who authorises a charge
// for a payment method. A pack lists its providers; each claims the
// methods it handles ("card", "cash", ...), and a job's paymentMethod must
// be one of them.
//
// Built-ins: simulated (a stand-in gateway that declines some charges, so
// the cancel and refund paths get exercised) and cash. Real gateways are
// Phase E; they register the same way.
package payment

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Charge is one authorisation request.
type Charge struct {
	JobID       string
	AmountCents int64
	Method      string
}

// Result of an authorisation. Declined is a normal outcome, not an error:
// errors are for "couldn't ask" (the gateway was down), which retries.
type Result struct {
	Approved bool
	Reason   string // why it was declined
}

type Provider interface {
	Methods() []string
	Authorize(ctx context.Context, c Charge) (Result, error)
}

var registry = strategy.NewRegistry[Provider]("payment")

func Register(name string, f strategy.Factory[Provider]) { registry.Register(name, f) }
func Build(name string, p strategy.Params) (Provider, error) {
	return registry.Build(name, p)
}
func Names() []string { return registry.Names() }

// Set is a pack's providers, looked up by payment method.
type Set struct{ byMethod map[string]Provider }

// NewSet refuses two providers claiming one method: which one would charge
// the customer would depend on list order.
func NewSet(providers map[string]Provider) (*Set, error) {
	s := &Set{byMethod: map[string]Provider{}}
	owner := map[string]string{}
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, m := range providers[name].Methods() {
			if prev, dup := owner[m]; dup {
				return nil, fmt.Errorf("payment method %q is claimed by both %s and %s", m, prev, name)
			}
			owner[m] = name
			s.byMethod[m] = providers[name]
		}
	}
	if len(s.byMethod) == 0 {
		return nil, fmt.Errorf("no payment methods: a pack needs at least one payment provider")
	}
	return s, nil
}

// For returns the provider for method.
func (s *Set) For(method string) (Provider, bool) {
	p, ok := s.byMethod[method]
	return p, ok
}

// Methods lists the accepted payment methods, sorted.
func (s *Set) Methods() []string {
	out := make([]string, 0, len(s.byMethod))
	for m := range s.byMethod {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func init() {
	Register("simulated", newSimulated)
	Register("cash", newCash)
}

// --- simulated: approves SuccessRatePct of charges at random ---

type simulated struct {
	SuccessRatePct int      `yaml:"success_rate_pct"`
	MethodList     []string `yaml:"methods"`
}

func newSimulated(p strategy.Params) (Provider, error) {
	s := &simulated{SuccessRatePct: 100, MethodList: []string{"card"}}
	if err := p.Decode(s); err != nil {
		return nil, err
	}
	if s.SuccessRatePct < 0 || s.SuccessRatePct > 100 {
		return nil, fmt.Errorf("success_rate_pct must be 0-100, got %d", s.SuccessRatePct)
	}
	return s, nil
}

func (s *simulated) Methods() []string { return s.MethodList }

func (s *simulated) Authorize(_ context.Context, c Charge) (Result, error) {
	if c.AmountCents <= 0 {
		return Result{Reason: "invalid amount"}, nil
	}
	if rand.IntN(100) < s.SuccessRatePct {
		return Result{Approved: true}, nil
	}
	return Result{Reason: "issuer declined the transaction"}, nil
}

// --- cash: nothing to authorise, the agent collects on delivery ---

type cash struct {
	MethodList []string `yaml:"methods"`
}

func newCash(p strategy.Params) (Provider, error) {
	c := &cash{MethodList: []string{"cash"}}
	if err := p.Decode(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *cash) Methods() []string { return c.MethodList }

func (c *cash) Authorize(_ context.Context, ch Charge) (Result, error) {
	if ch.AmountCents <= 0 {
		return Result{Reason: "invalid amount"}, nil
	}
	return Result{Approved: true}, nil
}
