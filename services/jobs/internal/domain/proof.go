package domain

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

// ProofPIN is the proof kind a customer answers with a code.
const ProofPIN = "pin"

// RequireProof marks each stop with the proof its kind needs (the pack's
// job.proof, e.g. {dropoff: photo}) and, for a PIN, generates the code:
// the customer sees it, dispatch only gets its hash.
func RequireProof(jobID string, stops []Stop, byKind map[string]string, newPIN func() (string, error)) error {
	for i := range stops {
		kind := byKind[stops[i].Kind]
		if kind == "" {
			continue
		}
		stops[i].Proof = kind
		if kind != ProofPIN {
			continue
		}
		pin, err := newPIN()
		if err != nil {
			return fmt.Errorf("generate PIN for stop %d of job %s: %w", stops[i].Seq, jobID, err)
		}
		stops[i].PIN = pin
		stops[i].PinHash = events.PinHash(jobID, stops[i].Seq, pin)
	}
	return nil
}

// NewPIN is a 4-digit code from crypto/rand: short enough to read out on a
// doorstep, and the attempt limit in dispatch is what keeps it safe.
func NewPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}
