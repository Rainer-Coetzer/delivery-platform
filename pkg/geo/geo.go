// Package geo is the little geometry the platform needs: straight-line
// distance between WGS84 points. Pricing uses it for per-km fares and
// zones, dispatch for ETAs and the location simulation.
package geo

import "math"

const earthRadiusMeters = 6371000.0

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
