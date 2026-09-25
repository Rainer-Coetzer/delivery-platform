// Package eta is the ETA strategy kind: minutes for an agent to cover a
// distance. Built-in: vehicle_speed.
package eta

import (
	"fmt"
	"math"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Estimator turns a straight-line distance into whole minutes, never less
// than one: "0 minutes" for an agent who has only just set off reads as a
// bug to a customer.
type Estimator interface {
	Minutes(meters float64, vehicle string) int
}

var registry = strategy.NewRegistry[Estimator]("eta")

func Register(name string, f strategy.Factory[Estimator]) { registry.Register(name, f) }
func Build(name string, p strategy.Params) (Estimator, error) {
	return registry.Build(name, p)
}
func Names() []string { return registry.Names() }

func init() { Register("vehicle_speed", newVehicleSpeed) }

// vehicleSpeed is an average speed per vehicle type, in metres per second,
// with a default for vehicles not listed.
type vehicleSpeed struct {
	DefaultMps float64            `yaml:"default_mps"`
	ByVehicle  map[string]float64 `yaml:"by_vehicle"`
}

func newVehicleSpeed(p strategy.Params) (Estimator, error) {
	v := &vehicleSpeed{}
	if err := p.Decode(v); err != nil {
		return nil, err
	}
	if v.DefaultMps <= 0 {
		return nil, fmt.Errorf("default_mps must be > 0, got %v", v.DefaultMps)
	}
	for name, mps := range v.ByVehicle {
		if mps <= 0 {
			return nil, fmt.Errorf("by_vehicle.%s must be > 0, got %v", name, mps)
		}
	}
	return v, nil
}

func (v *vehicleSpeed) Minutes(meters float64, vehicle string) int {
	mps := v.DefaultMps
	if s, ok := v.ByVehicle[vehicle]; ok {
		mps = s
	}
	return max(int(math.Ceil(meters/mps/60)), 1)
}
