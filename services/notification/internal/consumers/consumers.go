// Package consumers turns every job, payment and dispatch event into a
// human-readable notification for the customer, merchant or agent
// involved: stored for the in-app inbox, and sent over each of the pack's
// notify channels (pkg/strategy/notify).
package consumers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/notify"
	"github.com/rainercoetzer/delivery-platform/services/notification/internal/store"
)

type Handlers struct {
	Pool     *db.Pool
	Log      *slog.Logger
	Channels []notify.Channel
	Words    Words // the pack
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	n, err := build(env, h.Words)
	if err != nil {
		return err
	}
	if n == nil {
		return nil // event we don't notify on, e.g. an unrecognized topic
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		n.ID = uuid.NewString()
		if err := store.Insert(ctx, tx, n); err != nil {
			return err
		}
		// Sent inside the transaction: a channel that fails rolls back the
		// inbox row and the event retries. The channels that did succeed
		// send again on the retry, so delivery is at-least-once. Real
		// channels (Phase E) should be idempotent on the notification id.
		msg := notify.Message{Recipient: n.Recipient, JobID: n.JobID, Title: n.Title, Body: n.Body}
		for _, ch := range h.Channels {
			if err := ch.Send(ctx, msg); err != nil {
				return fmt.Errorf("send notification %s: %w", n.ID, err)
			}
		}
		return nil
	})
}

// Words is where notification texts get their nouns and state labels: the
// pack's terminology, so a courier customer reads "delivery" and "courier"
// where a food customer reads "order" and "driver". Stored notifications
// are in the pack's English; per-user locales come with i18n (Phase D).
type Words interface {
	Noun(locale, key string, title bool) string
	StateLabel(locale, state string) string
}

const locale = "en"

// build maps an event to the notification it produces, or nil if this
// service does not notify on that topic (e.g. the high-frequency
// dispatch.location stream).
func build(env events.Envelope, w Words) (*store.Notification, error) {
	job, Job := w.Noun(locale, "job", false), w.Noun(locale, "job", true)
	switch env.EventType {
	case events.JobCreated:
		d, err := events.Decode[events.JobCreatedData](env)
		if err != nil {
			return nil, err
		}
		if d.MerchantID == "" {
			return nil, nil // a courier job has no merchant to tell
		}
		// Known debt: the currency is hardcoded until the platform profile
		// (Phase D) gives one.
		return &store.Notification{
			Recipient: "merchant:" + d.MerchantID, JobID: d.JobID, Channel: "in-app",
			Title: "New " + job, Body: fmt.Sprintf("%s %s from %s, total N$%.2f", Job, short(d.JobID), d.CustomerName, cents(d.TotalCents)),
		}, nil
	case events.JobStatusChanged:
		d, err := events.Decode[events.JobStatusChangedData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{
			Recipient: "user:" + d.CustomerID, JobID: d.JobID, Channel: "in-app",
			Title: Job + " update", Body: fmt.Sprintf("Your %s %s: %s", job, short(d.JobID), w.StateLabel(locale, d.To)),
		}, nil
	case events.JobCancelled:
		d, err := events.Decode[events.JobCancelledData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{
			Recipient: "user:" + d.CustomerID, JobID: d.JobID, Channel: "in-app",
			Title: Job + " cancelled", Body: fmt.Sprintf("Your %s %s was cancelled: %s", job, short(d.JobID), d.Reason),
		}, nil
	case events.MerchantAccepted:
		d, err := events.Decode[events.MerchantDecisionData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "job:" + d.JobID, JobID: d.JobID, Channel: "in-app",
			Title: Job + " accepted", Body: fmt.Sprintf("The %s accepted %s %s", w.Noun(locale, "merchant", false), job, short(d.JobID))}, nil
	case events.MerchantRejected:
		d, err := events.Decode[events.MerchantDecisionData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "job:" + d.JobID, JobID: d.JobID, Channel: "in-app",
			Title: Job + " rejected", Body: fmt.Sprintf("The %s rejected %s %s: %s", w.Noun(locale, "merchant", false), job, short(d.JobID), d.Reason)}, nil
	case events.PaymentFailed:
		d, err := events.Decode[events.PaymentData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "job:" + d.JobID, JobID: d.JobID, Channel: "in-app",
			Title: "Payment failed", Body: fmt.Sprintf("Payment for %s %s failed: %s", job, short(d.JobID), d.Reason)}, nil
	case events.DispatchAssigned:
		d, err := events.Decode[events.AssignedData](env)
		if err != nil {
			return nil, err
		}
		return &store.Notification{Recipient: "job:" + d.JobID, JobID: d.JobID, Channel: "in-app",
			Title: w.Noun(locale, "agent", true) + " assigned", Body: fmt.Sprintf("%s is on the way, ETA %d min", d.AgentName, d.EtaMinutes)}, nil
	default:
		return nil, nil
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func cents(c int64) float64 { return float64(c) / 100 }
