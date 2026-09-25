// Package domain holds the Job aggregate, free of any HTTP or bus concerns
// so its rules can be unit tested on their own. Which states a job moves
// through is not decided here: that is the pack's lifecycle
// (packs/<name>/lifecycle.yaml, run by pkg/lifecycle). This package keeps
// the job's state, milestone and facts in step with what the machine says.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
)

// ErrNoSuchStop: an event names a stop the job doesn't have.
var ErrNoSuchStop = errors.New("job has no such stop")

type Item struct {
	CatalogueItemID string `json:"catalogueItemId"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	UnitPriceCents  int64  `json:"unitPriceCents"`
}

// Stop is a place the job visits, plus when the agent finished it.
type Stop struct {
	events.Stop
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// Job is returned directly by the REST API (see internal/api), so its
// json tags are the API contract's response shape: camelCase, matching
// pkg/events.
type Job struct {
	ID           string `json:"id"`
	Pack         string `json:"pack"`
	CustomerID   string `json:"customerId"`
	CustomerName string `json:"customerName"`
	MerchantID   string `json:"merchantId,omitempty"`
	MerchantName string `json:"merchantName,omitempty"`
	// Status is the pack's state name (food: PREPARING). Milestone is the
	// last milestone the job reached, which is what code decides on.
	Status           string          `json:"status"`
	Milestone        string          `json:"milestone"`
	Facts            map[string]bool `json:"facts"`
	Items            []Item          `json:"items"`
	Stops            []Stop          `json:"stops"`
	SubtotalCents    int64           `json:"subtotalCents"`
	DeliveryFeeCents int64           `json:"deliveryFeeCents"`
	SurgeMultiplier  float64         `json:"surgeMultiplier"`
	TotalCents       int64           `json:"totalCents"`
	PaymentMethod    string          `json:"paymentMethod"`
	AgentID          string          `json:"agentId"`
	CancelReason     string          `json:"cancelReason,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	// Actions are the states the caller may move this job to (lifecycle
	// transitions with `by:` their role). Filled in per request by the API.
	Actions []string `json:"actions,omitempty"`
}

// Apply runs trigger through the pack's machine and, if it moved or set a
// fact, updates the job. The steps come back so the caller can record and
// publish each one. reason is kept as the cancel reason if a step reaches
// the cancelled milestone.
func (j *Job) Apply(m *lifecycle.Machine, t lifecycle.Trigger, reason string) ([]lifecycle.Step, error) {
	steps, facts, err := m.Apply(j.Status, j.Facts, t)
	if err != nil {
		return nil, err
	}
	j.Facts = facts
	j.follow(steps, reason)
	return steps, nil
}

// Settle takes the automatic transitions open to a job that was just
// created. Food has none (it waits for payment and acceptance).
func (j *Job) Settle(m *lifecycle.Machine) []lifecycle.Step {
	steps := m.Settle(j.Status, j.Facts)
	j.follow(steps, "")
	return steps
}

func (j *Job) follow(steps []lifecycle.Step, reason string) {
	for _, s := range steps {
		j.Status = s.To
		if s.Milestone != "" {
			j.Milestone = s.Milestone
		}
		if s.Milestone == lifecycle.Cancelled {
			j.CancelReason = reason
		}
	}
}

// Terminal reports whether the job has finished, one way or the other.
func (j *Job) Terminal() bool {
	return j.Milestone == lifecycle.Completed || j.Milestone == lifecycle.Cancelled
}

// CompleteStop marks stop seq done at `at` and returns the stop context
// lifecycle triggers filter on. Completing a stop twice is refused: the
// agent app retrying must not move the job twice.
func (j *Job) CompleteStop(seq int, at time.Time) (lifecycle.StopInfo, error) {
	idx := -1
	for i, s := range j.Stops {
		if s.Seq == seq {
			idx = i
		}
	}
	if idx < 0 {
		return lifecycle.StopInfo{}, fmt.Errorf("%w: job %s, stop %d", ErrNoSuchStop, j.ID, seq)
	}
	if j.Stops[idx].CompletedAt != nil {
		return lifecycle.StopInfo{}, fmt.Errorf("job %s: stop %d is already completed", j.ID, seq)
	}
	j.Stops[idx].CompletedAt = &at
	return lifecycle.StopInfo{FirstPickup: seq == j.firstPickupSeq(), LastStop: seq == j.lastSeq()}, nil
}

func (j *Job) firstPickupSeq() int {
	first := -1
	for _, s := range j.Stops {
		if s.Kind == events.StopPickup && (first < 0 || s.Seq < first) {
			first = s.Seq
		}
	}
	return first
}

func (j *Job) lastSeq() int {
	last := -1
	for _, s := range j.Stops {
		last = max(last, s.Seq)
	}
	return last
}

// EventStops is the stops in their event shape, for jobs.created and the
// step into ready_for_dispatch.
func (j *Job) EventStops() []events.Stop {
	out := make([]events.Stop, 0, len(j.Stops))
	for _, s := range j.Stops {
		out = append(out, s.Stop)
	}
	return out
}

// SurgeMultiplierFor computes a delivery-fee multiplier from current system
// load: more open jobs per free agent pushes the multiplier up, capped so
// it never becomes punitive. Moves to the pricing strategies (Phase C
// step 4).
func SurgeMultiplierFor(openJobs, freeAgents int) float64 {
	if freeAgents <= 0 {
		freeAgents = 1
	}
	ratio := float64(openJobs) / float64(freeAgents)
	switch {
	case ratio <= 1:
		return 1.0
	case ratio <= 2:
		return 1.2
	case ratio <= 4:
		return 1.5
	default:
		return 2.0
	}
}
