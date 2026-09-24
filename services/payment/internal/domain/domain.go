// Package domain holds the simulated payment-authorization rule, isolated
// so it can be swapped for a real processor later without touching the
// consumer or storage code.
package domain

import "math/rand/v2"

// Authorize simulates a payment gateway: cash always succeeds (nothing to
// authorize), and card/mobile-money payments succeed most of the time but
// fail occasionally so the cancellation and refund paths are exercised.
func Authorize(method string, amountCents int64) (approved bool, reason string) {
	if amountCents <= 0 {
		return false, "invalid amount"
	}
	if method == "cash" {
		return true, ""
	}
	const successRate = 0.9
	if rand.Float64() < successRate {
		return true, ""
	}
	return false, "issuer declined the transaction"
}
