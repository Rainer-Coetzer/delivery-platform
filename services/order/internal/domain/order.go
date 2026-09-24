// Package domain holds the Order aggregate and its state machine, free of
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
	MenuItemID     string `json:"menuItemId"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unitPriceCents"`
}

// Order is returned directly by the REST API (see internal/api), so its
// json tags are the API contract's response shape — camelCase, matching
// pkg/events and docs/API-CONTRACT.md.
type Order struct {
	ID                 string          `json:"id"`
	CustomerID         string          `json:"customerId"`
	CustomerName       string          `json:"customerName"`
	RestaurantID       string          `json:"restaurantId"`
	RestaurantName     string          `json:"restaurantName"`
	Status             string          `json:"status"`
	Paid               bool            `json:"paid"`
	RestaurantAccepted bool            `json:"restaurantAccepted"`
	Items              []Item          `json:"items"`
	SubtotalCents      int64           `json:"subtotalCents"`
	DeliveryFeeCents   int64           `json:"deliveryFeeCents"`
	SurgeMultiplier    float64         `json:"surgeMultiplier"`
	TotalCents         int64           `json:"totalCents"`
	PaymentMethod      string          `json:"paymentMethod"`
	Pickup             events.Location `json:"pickup"`
	Dropoff            events.Location `json:"dropoff"`
	DriverID           string          `json:"driverId"`
	CancelReason       string          `json:"cancelReason,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

// Transition moves the order to `to`, returning the previous status so the
// caller can build an OrderStatusChangedData event. It refuses transitions
// the state machine does not allow, which also makes duplicate event
// deliveries harmless (e.g. two payments.completed events both trying to
// move CONFIRMED -> CONFIRMED fail safely rather than corrupting history).
func (o *Order) Transition(to, reason string) (from string, err error) {
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
// restaurant have said yes, then immediately on to PREPARING — the kitchen
// starts as soon as an order is confirmed, so there is no separate
// "start preparing" signal in this system.
func (o *Order) MaybeConfirm() (transitioned bool, from string, err error) {
	if o.Status != events.StatusCreated || !o.Paid || !o.RestaurantAccepted {
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
// load: more open orders per free driver pushes the multiplier up, capped so
// it never becomes punitive. This is a pure function so it's easy to test
// against fixed inputs.
func SurgeMultiplierFor(openOrders, freeDrivers int) float64 {
	if freeDrivers <= 0 {
		freeDrivers = 1
	}
	ratio := float64(openOrders) / float64(freeDrivers)
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
