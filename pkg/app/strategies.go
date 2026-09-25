package app

import (
	"fmt"

	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/dispatch"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/eta"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/notify"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/payment"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/pricing"
)

// Strategies are the pack's strategy choices, built once at boot (ADR-0004
// tier 2). Every module gets the same set; each uses the kinds it needs.
type Strategies struct {
	Pricing  pricing.Policy
	Dispatch dispatch.Strategy
	ETA      eta.Estimator
	Payments *payment.Set
	Notify   []notify.Channel
}

// BuildStrategies builds every strategy the pack names. Any problem (an
// unknown name, a param typo, a bad rate) stops the boot with the reason,
// so a misconfigured pack never takes a job.
func BuildStrategies(p *packs.Pack) (*Strategies, error) {
	s := p.Strategies
	for kind, name := range map[string]string{"pricing": s.Pricing.Name, "dispatch": s.Dispatch.Name, "eta": s.ETA.Name} {
		if name == "" {
			return nil, fmt.Errorf("pack %s: strategies.%s is required", p.Name, kind)
		}
	}
	params := func(kind string, st packs.Strategy) strategy.Params {
		return strategy.NewParams(fmt.Sprintf("pack %s: %s %s", p.Name, kind, st.Name), &st.Params)
	}

	var out Strategies
	var err error
	if out.Pricing, err = pricing.Build(s.Pricing.Name, params("pricing", s.Pricing)); err != nil {
		return nil, fmt.Errorf("pack %s: %w", p.Name, err)
	}
	if out.Dispatch, err = dispatch.Build(s.Dispatch.Name, params("dispatch", s.Dispatch)); err != nil {
		return nil, fmt.Errorf("pack %s: %w", p.Name, err)
	}
	if s.Dispatch.Name == "capacity_aware" && !p.Has(packs.ModCapacity) {
		return nil, fmt.Errorf("pack %s: dispatch capacity_aware needs the capacity module", p.Name)
	}
	if out.ETA, err = eta.Build(s.ETA.Name, params("eta", s.ETA)); err != nil {
		return nil, fmt.Errorf("pack %s: %w", p.Name, err)
	}
	providers := map[string]payment.Provider{}
	for _, st := range s.Payment {
		if _, dup := providers[st.Name]; dup {
			return nil, fmt.Errorf("pack %s: payment provider %s is listed twice", p.Name, st.Name)
		}
		if providers[st.Name], err = payment.Build(st.Name, params("payment", st)); err != nil {
			return nil, fmt.Errorf("pack %s: %w", p.Name, err)
		}
	}
	if out.Payments, err = payment.NewSet(providers); err != nil {
		return nil, fmt.Errorf("pack %s: %w", p.Name, err)
	}
	for _, st := range s.Notify {
		ch, err := notify.Build(st.Name, params("notify", st))
		if err != nil {
			return nil, fmt.Errorf("pack %s: %w", p.Name, err)
		}
		out.Notify = append(out.Notify, ch)
	}
	return &out, nil
}
