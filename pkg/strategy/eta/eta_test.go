package eta

import (
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
)

func TestVehicleSpeed(t *testing.T) {
	e, err := Build("vehicle_speed", strategytest.Params(t, "eta", `{default_mps: 8.3, by_vehicle: {car: 12.5}}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		meters  float64
		vehicle string
		want    int
	}{
		{0, "bike", 1},        // never "0 minutes"
		{4980, "bike", 10},    // 600 s at the default speed
		{4981, "bike", 11},    // rounds up
		{7500, "car", 10},     // the vehicle's own speed
		{4980, "scooter", 10}, // unlisted vehicle: the default
	}
	for _, c := range cases {
		if got := e.Minutes(c.meters, c.vehicle); got != c.want {
			t.Errorf("%v m by %s = %d min, want %d", c.meters, c.vehicle, got, c.want)
		}
	}
}

func TestVehicleSpeedBootErrors(t *testing.T) {
	for params, want := range map[string]string{
		`{}`:                                     "default_mps must be > 0",
		`{default_mps: 8, by_vehicle: {car: 0}}`: "by_vehicle.car must be > 0",
		`{default_mps: 8, speed: 3}`:             "field speed not found",
	} {
		if _, err := Build("vehicle_speed", strategytest.Params(t, "eta", params)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", params, want, err)
		}
	}
}
