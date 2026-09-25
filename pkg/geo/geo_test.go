package geo

import "testing"

func TestHaversineMeters_SamePoint(t *testing.T) {
	if d := HaversineMeters(-22.5594, 17.0832, -22.5594, 17.0832); d != 0 {
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
