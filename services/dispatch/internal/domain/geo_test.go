package domain

import "testing"

func TestHaversineMeters_SamePoint(t *testing.T) {
	d := HaversineMeters(-22.5594, 17.0832, -22.5594, 17.0832)
	if d != 0 {
		t.Fatalf("distance between identical points = %v, want 0", d)
	}
}

func TestHaversineMeters_KnownDistance(t *testing.T) {
	// Windhoek CBD to Klein Windhoek, roughly 4-5 km apart.
	d := HaversineMeters(-22.5700, 17.0836, -22.5390, 17.1140)
	if d < 3000 || d > 6000 {
		t.Fatalf("distance = %v meters, want roughly 3000-6000", d)
	}
}

func TestETAMinutes_NeverZero(t *testing.T) {
	if got := ETAMinutes(1); got < 1 {
		t.Fatalf("ETAMinutes(1) = %d, want >= 1", got)
	}
}

func TestStepToward_ArrivesWhenCloseEnough(t *testing.T) {
	_, _, arrived := StepToward(0, 0, 0.00001, 0.00001, 1000)
	if !arrived {
		t.Fatal("expected to arrive when the remaining distance is smaller than the step")
	}
}

func TestStepToward_MovesPartway(t *testing.T) {
	lat, lng, arrived := StepToward(0, 0, 1, 1, 1000)
	if arrived {
		t.Fatal("should not arrive in one small step over a large distance")
	}
	if lat <= 0 || lng <= 0 {
		t.Fatalf("expected movement toward target, got lat=%v lng=%v", lat, lng)
	}
}
