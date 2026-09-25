// Package domain holds the dispatch-side movement math: stepping a point
// a fixed distance toward a target, the core of the agent-location
// simulation. Pure and unit-testable without a database. Distances come
// from pkg/geo, ETAs from the pack's eta strategy.
package domain

import "github.com/rainercoetzer/delivery-platform/pkg/geo"

// StepToward moves (lat, lng) toward (targetLat, targetLng) by at most
// stepMeters, returning the new position and whether the target was
// reached (within one step). Used to simulate an agent's GPS ticking
// forward every couple of seconds.
func StepToward(lat, lng, targetLat, targetLng, stepMeters float64) (newLat, newLng float64, arrived bool) {
	dist := geo.HaversineMeters(lat, lng, targetLat, targetLng)
	if dist <= stepMeters || dist == 0 {
		return targetLat, targetLng, true
	}
	frac := stepMeters / dist
	return lat + (targetLat-lat)*frac, lng + (targetLng-lng)*frac, false
}
