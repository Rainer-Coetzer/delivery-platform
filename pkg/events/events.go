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

// Topics. Every topic is keyed by jobId so one job's events stay on one
// partition and are consumed in the order they were written. The exceptions
// are keyed by the entity named in the comment.
const (
	JobCreated       = "jobs.created"
	JobStatusChanged = "jobs.status-changed"
	JobCancelled     = "jobs.cancelled"

	MerchantAccepted = "merchant.job-accepted"
	MerchantRejected = "merchant.job-rejected"
	MerchantReady    = "merchant.job-ready"

	PaymentCompleted = "payments.completed"
	PaymentFailed    = "payments.failed"
	PaymentRefunded  = "payments.refunded"

	DispatchAssigned  = "dispatch.assigned"
	DispatchPickedUp  = "dispatch.picked-up"
	DispatchCompleted = "dispatch.completed"
	DispatchLocation  = "dispatch.location" // keyed by agentId
)

// AllTopics is what infra creates on startup and what the catch-all
// consumers (notification, reporting) subscribe to.
var AllTopics = []string{
	JobCreated, JobStatusChanged, JobCancelled,
	MerchantAccepted, MerchantRejected, MerchantReady,
	PaymentCompleted, PaymentFailed, PaymentRefunded,
	DispatchAssigned, DispatchPickedUp, DispatchCompleted, DispatchLocation,
}

// Envelope wraps every Kafka message.
type Envelope struct {
	EventID   string          `json:"eventId"`
	EventType string          `json:"eventType"`
	JobID     string          `json:"jobId,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// New builds an envelope around data. jobID may be empty for events that are
// not about a job (agent location).
func New(eventType, jobID string, data any) (Envelope, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s: %w", eventType, err)
	}
	return Envelope{
		EventID:   uuid.NewString(),
		EventType: eventType,
		JobID:     jobID,
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

// Job statuses.
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

type JobLine struct {
	CatalogueItemID string `json:"catalogueItemId"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	UnitPriceCents  int64  `json:"unitPriceCents"`
}

type JobCreatedData struct {
	JobID            string    `json:"jobId"`
	CustomerID       string    `json:"customerId"`
	CustomerName     string    `json:"customerName"`
	MerchantID       string    `json:"merchantId"`
	MerchantName     string    `json:"merchantName"`
	Items            []JobLine `json:"items"`
	SubtotalCents    int64     `json:"subtotalCents"`
	DeliveryFeeCents int64     `json:"deliveryFeeCents"`
	SurgeMultiplier  float64   `json:"surgeMultiplier"`
	TotalCents       int64     `json:"totalCents"`
	PaymentMethod    string    `json:"paymentMethod"`
	Pickup           Location  `json:"pickup"`
	Dropoff          Location  `json:"dropoff"`
}

type JobStatusChangedData struct {
	JobID      string `json:"jobId"`
	CustomerID string `json:"customerId"`
	MerchantID string `json:"merchantId"`
	AgentID    string `json:"agentId,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason,omitempty"`
}

type JobCancelledData struct {
	JobID      string    `json:"jobId"`
	CustomerID string    `json:"customerId"`
	MerchantID string    `json:"merchantId"`
	Items      []JobLine `json:"items"`
	Reason     string    `json:"reason"`
}

type MerchantDecisionData struct {
	JobID       string `json:"jobId"`
	MerchantID  string `json:"merchantId"`
	PrepMinutes int    `json:"prepMinutes,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type MerchantReadyData struct {
	JobID      string   `json:"jobId"`
	MerchantID string   `json:"merchantId"`
	Pickup     Location `json:"pickup"`
	Dropoff    Location `json:"dropoff"`
}

type PaymentData struct {
	JobID       string `json:"jobId"`
	PaymentID   string `json:"paymentId,omitempty"`
	AmountCents int64  `json:"amountCents"`
	Reason      string `json:"reason,omitempty"`
}

type AssignedData struct {
	JobID        string   `json:"jobId"`
	AssignmentID string   `json:"assignmentId"`
	AgentID      string   `json:"agentId"`
	AgentName    string   `json:"agentName"`
	EtaMinutes   int      `json:"etaMinutes"`
	AgentAt      Location `json:"agentAt"`
}

type AssignmentProgressData struct {
	JobID           string `json:"jobId"`
	AssignmentID    string `json:"assignmentId"`
	AgentID         string `json:"agentId"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
}

type AgentLocationData struct {
	AgentID    string  `json:"agentId"`
	JobID      string  `json:"jobId,omitempty"`
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
	Recipient string `json:"recipient"` // "user:<id>" or "merchant:<id>"
	JobID     string `json:"jobId,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Channel   string `json:"channel"`
}
