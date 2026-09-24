// Package events holds the Kafka contract shared by every service: topic
// names, the envelope every message is wrapped in, and the payload of each
// topic. Changing anything here is a breaking change for consumers.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Topics. Every topic is keyed by orderId so one order's events stay on one
// partition and are consumed in the order they were written. The exceptions
// are keyed by the entity named in the comment.
const (
	OrderCreated       = "orders.created"
	OrderStatusChanged = "orders.status-changed"
	OrderCancelled     = "orders.cancelled"

	RestaurantAccepted  = "restaurant.order-accepted"
	RestaurantRejected  = "restaurant.order-rejected"
	RestaurantPreparing = "restaurant.order-preparing"
	RestaurantReady     = "restaurant.order-ready"

	PaymentCompleted = "payments.completed"
	PaymentFailed    = "payments.failed"
	PaymentRefunded  = "payments.refunded"

	DeliveryAssigned  = "delivery.assigned"
	DeliveryPickedUp  = "delivery.picked-up"
	DeliveryCompleted = "delivery.completed"
	DeliveryLocation  = "delivery.location" // keyed by driverId

	UserRegistered      = "users.registered"      // keyed by userId
	NotificationCreated = "notifications.created" // keyed by recipient
)

// AllTopics is what infra creates on startup and what the catch-all
// consumers (notification, admin) subscribe to.
var AllTopics = []string{
	OrderCreated, OrderStatusChanged, OrderCancelled,
	RestaurantAccepted, RestaurantRejected, RestaurantPreparing, RestaurantReady,
	PaymentCompleted, PaymentFailed, PaymentRefunded,
	DeliveryAssigned, DeliveryPickedUp, DeliveryCompleted, DeliveryLocation,
	UserRegistered, NotificationCreated,
}

// Envelope wraps every Kafka message.
type Envelope struct {
	EventID   string          `json:"eventId"`
	EventType string          `json:"eventType"`
	OrderID   string          `json:"orderId,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// New builds an envelope around data. orderID may be empty for events that are
// not about an order (driver location, user registration).
func New(eventType, orderID string, data any) (Envelope, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s: %w", eventType, err)
	}
	return Envelope{
		EventID:   uuid.NewString(),
		EventType: eventType,
		OrderID:   orderID,
		Timestamp: time.Now().UTC(),
		Data:      raw,
	}, nil
}

// Decode unmarshals the envelope's data into T.
func Decode[T any](env Envelope) (T, error) {
	var v T
	if err := json.Unmarshal(env.Data, &v); err != nil {
		return v, fmt.Errorf("decode %s %s: %w", env.EventType, env.EventID, err)
	}
	return v, nil
}

// Order statuses.
const (
	StatusCreated        = "CREATED"
	StatusConfirmed      = "CONFIRMED"
	StatusPreparing      = "PREPARING"
	StatusReady          = "READY"
	StatusOutForDelivery = "OUT_FOR_DELIVERY"
	StatusDelivered      = "DELIVERED"
	StatusCancelled      = "CANCELLED"
)

// Location is a WGS84 point with an optional human-readable label.
type Location struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label,omitempty"`
}

type OrderItem struct {
	MenuItemID     string `json:"menuItemId"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unitPriceCents"`
}

type OrderCreatedData struct {
	OrderID          string      `json:"orderId"`
	CustomerID       string      `json:"customerId"`
	CustomerName     string      `json:"customerName"`
	RestaurantID     string      `json:"restaurantId"`
	RestaurantName   string      `json:"restaurantName"`
	Items            []OrderItem `json:"items"`
	SubtotalCents    int64       `json:"subtotalCents"`
	DeliveryFeeCents int64       `json:"deliveryFeeCents"`
	SurgeMultiplier  float64     `json:"surgeMultiplier"`
	TotalCents       int64       `json:"totalCents"`
	PaymentMethod    string      `json:"paymentMethod"`
	Pickup           Location    `json:"pickup"`
	Dropoff          Location    `json:"dropoff"`
}

type OrderStatusChangedData struct {
	OrderID      string `json:"orderId"`
	CustomerID   string `json:"customerId"`
	RestaurantID string `json:"restaurantId"`
	DriverID     string `json:"driverId,omitempty"`
	From         string `json:"from"`
	To           string `json:"to"`
	Reason       string `json:"reason,omitempty"`
}

type OrderCancelledData struct {
	OrderID      string      `json:"orderId"`
	CustomerID   string      `json:"customerId"`
	RestaurantID string      `json:"restaurantId"`
	Items        []OrderItem `json:"items"`
	Reason       string      `json:"reason"`
}

type RestaurantDecisionData struct {
	OrderID      string `json:"orderId"`
	RestaurantID string `json:"restaurantId"`
	PrepMinutes  int    `json:"prepMinutes,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type RestaurantReadyData struct {
	OrderID      string   `json:"orderId"`
	RestaurantID string   `json:"restaurantId"`
	Pickup       Location `json:"pickup"`
	Dropoff      Location `json:"dropoff"`
}

type PaymentData struct {
	OrderID     string `json:"orderId"`
	PaymentID   string `json:"paymentId,omitempty"`
	AmountCents int64  `json:"amountCents"`
	Reason      string `json:"reason,omitempty"`
}

type DeliveryAssignedData struct {
	OrderID    string   `json:"orderId"`
	DeliveryID string   `json:"deliveryId"`
	DriverID   string   `json:"driverId"`
	DriverName string   `json:"driverName"`
	EtaMinutes int      `json:"etaMinutes"`
	DriverAt   Location `json:"driverAt"`
}

type DeliveryProgressData struct {
	OrderID         string `json:"orderId"`
	DeliveryID      string `json:"deliveryId"`
	DriverID        string `json:"driverId"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
}

type DriverLocationData struct {
	DriverID   string  `json:"driverId"`
	OrderID    string  `json:"orderId,omitempty"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	EtaMinutes int     `json:"etaMinutes,omitempty"`
}

type UserRegisteredData struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type NotificationData struct {
	ID        string `json:"id"`
	Recipient string `json:"recipient"` // "user:<id>" or "restaurant:<id>"
	OrderID   string `json:"orderId,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Channel   string `json:"channel"`
}
