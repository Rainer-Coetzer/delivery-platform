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

	DispatchAssigned      = "dispatch.assigned"
	DispatchStopCompleted = "dispatch.stop-completed"
	DispatchLocation      = "dispatch.location" // keyed by agentId
)

// AllTopics is what infra creates on startup and what the catch-all
// consumers (notification, reporting) subscribe to.
var AllTopics = []string{
	JobCreated, JobStatusChanged, JobCancelled,
	MerchantAccepted, MerchantRejected, MerchantReady,
	PaymentCompleted, PaymentFailed, PaymentRefunded,
	DispatchAssigned, DispatchStopCompleted, DispatchLocation,
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

// Job states are the pack's own names (packs/<name>/lifecycle.yaml); what
// consumers react to is the milestone, pkg/lifecycle.Created and friends.

// Location is a WGS84 point with an optional human-readable label.
type Location struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label,omitempty"`
}

// Stop kinds.
const (
	StopPickup  = "pickup"
	StopDropoff = "dropoff"
)

// Stop is one place a job visits, in visiting order (Seq from 0). A food
// job has two: pickup at the merchant, dropoff at the customer.
type Stop struct {
	Seq        int      `json:"seq"`
	Kind       string   `json:"kind"`
	Location   Location `json:"location"`
	MerchantID string   `json:"merchantId,omitempty"` // set when the stop is at a merchant
}

// JobLine is a catalogue item or, with the packages module, a package.
// Kind is empty on events from before packages existed: read it as
// "catalogue".
type JobLine struct {
	Kind            string `json:"kind,omitempty"`
	CatalogueItemID string `json:"catalogueItemId,omitempty"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	UnitPriceCents  int64  `json:"unitPriceCents"`
	WeightGrams     int64  `json:"weightGrams,omitempty"`
	VolumeCm3       int64  `json:"volumeCm3,omitempty"` // per unit
}

// JobSize is what a job's packages add up to, for vehicle capacity.
type JobSize struct {
	WeightGrams int64 `json:"weightGrams"`
	VolumeCm3   int64 `json:"volumeCm3"`
}

type JobCreatedData struct {
	JobID            string    `json:"jobId"`
	Pack             string    `json:"pack"`
	State            string    `json:"state"` // the pack's initial state
	CustomerID       string    `json:"customerId"`
	CustomerName     string    `json:"customerName"`
	MerchantID       string    `json:"merchantId,omitempty"`
	MerchantName     string    `json:"merchantName,omitempty"`
	Items            []JobLine `json:"items"`
	SubtotalCents    int64     `json:"subtotalCents"`
	DeliveryFeeCents int64     `json:"deliveryFeeCents"`
	SurgeMultiplier  float64   `json:"surgeMultiplier"`
	TotalCents       int64     `json:"totalCents"`
	PaymentMethod    string    `json:"paymentMethod"`
	Stops            []Stop    `json:"stops"`
}

// JobStatusChangedData is one lifecycle step. From and To are the pack's
// state names, for display; consumers decide on ToMilestone, which is empty
// when the new state maps no milestone (food's PREPARING).
type JobStatusChangedData struct {
	JobID       string `json:"jobId"`
	CustomerID  string `json:"customerId"`
	MerchantID  string `json:"merchantId,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	From        string `json:"from"`
	To          string `json:"to"`
	ToMilestone string `json:"toMilestone,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// Stops, Size and Requires are set only on the step into
	// ready_for_dispatch, so dispatch can route the job, and match it to a
	// vehicle, without keeping its own copy of every job.
	Stops    []Stop   `json:"stops,omitempty"`
	Size     *JobSize `json:"size,omitempty"`
	Requires []string `json:"requires,omitempty"` // agent capabilities, from the pack
}

type JobCancelledData struct {
	JobID      string    `json:"jobId"`
	CustomerID string    `json:"customerId"`
	MerchantID string    `json:"merchantId,omitempty"`
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
	JobID      string `json:"jobId"`
	MerchantID string `json:"merchantId"`
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

// StopCompletedData: the agent finished stop StopSeq of the job. The jobs
// module owns the stops, so it works out whether that was the first pickup
// or the last stop; dispatch only says which one.
type StopCompletedData struct {
	JobID        string `json:"jobId"`
	AssignmentID string `json:"assignmentId"`
	AgentID      string `json:"agentId"`
	StopSeq      int    `json:"stopSeq"`
	// DurationSeconds is assignment to this stop; reporting uses it on the
	// last stop as the assignment time.
	DurationSeconds int64 `json:"durationSeconds,omitempty"`
}

type AgentLocationData struct {
	AgentID    string  `json:"agentId"`
	JobID      string  `json:"jobId,omitempty"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	EtaMinutes int     `json:"etaMinutes,omitempty"`
}
