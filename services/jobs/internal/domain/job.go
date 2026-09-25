// Package domain holds the Job aggregate and its state machine, free of
// any HTTP or Kafka concerns so the transition rules can be unit tested on
// their own.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

var ErrInvalidTransition = errors.New("invalid order status transition")

// validTransitions maps each status to the statuses it may move to next.
// CANCELLED and DELIVERED are terminal: they have no outgoing edges.
var validTransitions = map[string][]string{
	events.StatusCreated:        {events.StatusConfirmed, events.StatusCancelled},
	events.StatusConfirmed:      {events.StatusPreparing, events.StatusCancelled},
	events.StatusPreparing:      {events.StatusReady, events.StatusCancelled},
	events.StatusReady:          {events.StatusOutForDelivery, events.StatusCancelled},
	events.StatusOutForDelivery: {events.StatusDelivered, events.StatusCancelled},
	events.StatusDelivered:      {},
	events.StatusCancelled:      {},
}

// CanTransition reports whether moving from `from` to `to` is allowed.
func CanTransition(from, to string) bool {
	for _, next := range validTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

type Item struct {
	CatalogueItemID string `json:"catalogueItemId"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	UnitPriceCents  int64  `json:"unitPriceCents"`
}

// Job is returned directly by the REST API (see internal/api), so its
// json tags are the API contract's response shape — camelCase, matching
// pkg/events.
type Job struct {
	ID               string          `json:"id"`
	CustomerID       string          `json:"customerId"`
	CustomerName     string          `json:"customerName"`
	MerchantID       string          `json:"merchantId"`
	MerchantName     string          `json:"merchantName"`
	Status           string          `json:"status"`
	Paid             bool            `json:"paid"`
	MerchantAccepted bool            `json:"merchantAccepted"`
	Items            []Item          `json:"items"`
	SubtotalCents    int64           `json:"subtotalCents"`
	DeliveryFeeCents int64           `json:"deliveryFeeCents"`
	SurgeMultiplier  float64         `json:"surgeMultiplier"`
	TotalCents       int64           `json:"totalCents"`
	PaymentMethod    string          `json:"paymentMethod"`
	Pickup           events.Location `json:"pickup"`
	Dropoff          events.Location `json:"dropoff"`
	AgentID          string          `json:"agentId"`
	CancelReason     string          `json:"cancelReason,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

// Transition moves the job to `to`, returning the previous status so the
// caller can build an JobStatusChangedData event. It refuses transitions
// the state machine does not allow, which also makes duplicate event
// assignments harmless (e.g. two payments.completed events both trying to
// move CONFIRMED -> CONFIRMED fail safely rather than corrupting history).
func (o *Job) Transition(to, reason string) (from string, err error) {
	from = o.Status
	if from == to {
		return from, fmt.Errorf("%w: already %s", ErrInvalidTransition, to)
	}
	if !CanTransition(from, to) {
		return from, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	o.Status = to
	if to == events.StatusCancelled {
		o.CancelReason = reason
	}
	return from, nil
}

// MaybeConfirm moves CREATED -> CONFIRMED once both the payment and the
// merchant have said yes, then immediately on to PREPARING — the prep
// starts as soon as a job is confirmed, so there is no separate
// "start preparing" signal in this system.
func (o *Job) MaybeConfirm() (transitioned bool, from string, err error) {
	if o.Status != events.StatusCreated || !o.Paid || !o.MerchantAccepted {
		return false, "", nil
	}
	from, err = o.Transition(events.StatusConfirmed, "")
	if err != nil {
		return false, from, err
	}
	if _, err := o.Transition(events.StatusPreparing, ""); err != nil {
		return false, from, err
	}
	return true, from, nil
}

// SurgeMultiplierFor computes a delivery-fee multiplier from current system
// load: more open jobs per free agent pushes the multiplier up, capped so
// it never becomes punitive. This is a pure function so it's easy to test
// against fixed inputs.
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
