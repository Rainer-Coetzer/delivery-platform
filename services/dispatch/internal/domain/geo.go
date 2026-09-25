// Package domain holds the assignment-side geometry and ETA math: distance
// between two points, an ETA from that distance, and stepping a point a
// fixed distance toward a target — the core of the agent-location
// simulation. All of this is pure and unit-testable without a database.
package domain

import "math"

const earthRadiusMeters = 6371000.0
const avgSpeedMetersPerSecond = 8.3 // ~30 km/h, city driving with stops

// HaversineMeters returns the great-circle distance between two WGS84
// points in meters.
func HaversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

// ETAMinutes estimates travel time over a straight-line distance at the
// assumed average city speed, rounded up so "0 minutes" is never shown for
// an assignment that has genuinely just started.
func ETAMinutes(distanceMeters float64) int {
	seconds := distanceMeters / avgSpeedMetersPerSecond
	minutes := int(math.Ceil(seconds / 60))
	if minutes < 1 {
		return 1
	}
	return minutes
}

// StepToward moves (lat, lng) toward (targetLat, targetLng) by at most
// stepMeters, returning the new position and whether the target was
// reached (within one step). Used to simulate an agent's GPS ticking
// forward every couple of seconds.
func StepToward(lat, lng, targetLat, targetLng, stepMeters float64) (newLat, newLng float64, arrived bool) {
	dist := HaversineMeters(lat, lng, targetLat, targetLng)
	if dist <= stepMeters || dist == 0 {
		return targetLat, targetLng, true
	}
	frac := stepMeters / dist
	return lat + (targetLat-lat)*frac, lng + (targetLng-lng)*frac, false
}
