// Package consumers reacts to jobs.created (accept or reject based on
// opening hours and stock), jobs.status-changed (the prep starts once
// the job reaches the confirmed milestone) and jobs.cancelled (release any
// reserved stock).
package consumers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/outbox"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/store"
)

// Topics this service consumes, kept next to the handlers that use them.
var Topics = []string{events.JobCreated, events.JobStatusChanged, events.JobCancelled}

type Handlers struct {
	Pool *db.Pool
	Log  *slog.Logger
}

func (h *Handlers) Handle(ctx context.Context, env events.Envelope) error {
	switch env.EventType {
	case events.JobCreated:
		data, err := events.Decode[events.JobCreatedData](env)
		if err != nil {
			return err
		}
		return h.handleJobCreated(ctx, env, data)
	case events.JobStatusChanged:
		data, err := events.Decode[events.JobStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.handleStatusChanged(ctx, env, data)
	case events.JobCancelled:
		data, err := events.Decode[events.JobCancelledData](env)
		if err != nil {
			return err
		}
		return h.handleJobCancelled(ctx, env, data)
	default:
		return nil
	}
}

func (h *Handlers) handleJobCreated(ctx context.Context, env events.Envelope, data events.JobCreatedData) error {
	if data.MerchantID == "" {
		return nil // a job with no merchant stop (courier): nothing to accept
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}

		// An unknown merchant gets an explicit rejection. It used to fail
		// the lookup, retry, dead-letter, and leave the job CREATED
		// forever. No prep row is cached for it (merchant_id is a
		// foreign key), and the cancel handler tolerates that.
		rest, err := store.GetMerchantTx(ctx, tx, data.MerchantID)
		if errors.Is(err, store.ErrNotFound) {
			return publishDecision(ctx, tx, events.MerchantRejected, data.JobID, data.MerchantID, "merchant not found")
		}
		if err != nil {
			return err
		}

		if err := store.InsertMerchantJob(ctx, tx, &store.MerchantJob{
			JobID: data.JobID, MerchantID: data.MerchantID, Status: store.StatusPending,
			Items: data.Items,
		}); err != nil {
			return err
		}

		reason, err := h.decide(ctx, tx, rest, env, data)
		if err != nil {
			return err
		}
		if reason != "" {
			if err := store.UpdateMerchantJobStatus(ctx, tx, data.JobID, store.StatusRejected, reason); err != nil {
				return err
			}
			return publishDecision(ctx, tx, events.MerchantRejected, data.JobID, data.MerchantID, reason)
		}

		if err := store.DecrementStock(ctx, tx, requestedFrom(data.Items)); err != nil {
			return err
		}
		if err := store.UpdateMerchantJobStatus(ctx, tx, data.JobID, store.StatusAccepted, ""); err != nil {
			return err
		}
		return publishDecision(ctx, tx, events.MerchantAccepted, data.JobID, data.MerchantID, "")
	})
}

// decide returns a rejection reason, or "" to accept. Hours are judged in
// the merchant's own timezone at the moment the job was placed.
func (h *Handlers) decide(ctx context.Context, tx pgx.Tx, rest *domain.Merchant, env events.Envelope, data events.JobCreatedData) (string, error) {
	open, err := rest.OpenAtTime(env.Timestamp)
	if err != nil {
		// Bad data on our side, not the customer's: reject rather than
		// retry forever, and log it loudly so someone fixes the row.
		h.Log.Error("merchant timezone misconfigured", "merchantId", rest.ID, "error", err)
		return rest.Name + " can't take jobs right now", nil
	}
	if !open {
		return rest.Name + " is currently closed", nil
	}

	requested := requestedFrom(data.Items)
	ids := make([]string, 0, len(requested))
	for _, r := range requested {
		ids = append(ids, r.CatalogueItemID)
	}
	catalogue, err := store.CatalogueFor(ctx, tx, ids)
	if err != nil {
		return "", err
	}
	if ok, reason := domain.CheckItems(rest.ID, catalogue, requested); !ok {
		return reason, nil
	}
	return "", nil
}

func requestedFrom(items []events.JobLine) []domain.RequestedItem {
	out := make([]domain.RequestedItem, 0, len(items))
	for _, it := range items {
		out = append(out, domain.RequestedItem{CatalogueItemID: it.CatalogueItemID, Quantity: it.Quantity, UnitPriceCents: it.UnitPriceCents})
	}
	return out
}

func (h *Handlers) handleJobCancelled(ctx context.Context, env events.Envelope, data events.JobCancelledData) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}
		ro, err := store.GetMerchantJobForUpdate(ctx, tx, data.JobID)
		if errors.Is(err, store.ErrNotFound) {
			return nil // rejected as "merchant not found": nothing was cached or reserved
		}
		if err != nil {
			return err
		}
		// Only release stock if it was actually reserved (accepted and
		// beyond); a rejected job never touched stock.
		if ro.Status == store.StatusAccepted || ro.Status == store.StatusPreparing || ro.Status == store.StatusReady {
			if err := store.RestoreStock(ctx, tx, requestedFrom(ro.Items)); err != nil {
				return err
			}
		}
		return store.UpdateMerchantJobStatus(ctx, tx, data.JobID, store.StatusCancelled, "")
	})
}

// handleStatusChanged starts the prep when the job reaches the confirmed
// milestone: in food that is payment and acceptance both in. It keys on
// the milestone, not a state name, so a pack can call its states anything.
func (h *Handlers) handleStatusChanged(ctx context.Context, env events.Envelope, data events.JobStatusChangedData) error {
	if data.ToMilestone != lifecycle.Confirmed {
		return nil
	}
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		moved, err := store.MarkPreparing(ctx, tx, data.JobID, env.Timestamp)
		if err != nil {
			return err
		}
		if !moved {
			h.Log.Warn("confirmed for a job this merchant hasn't accepted, ignoring", "jobId", data.JobID)
		}
		return nil
	})
}

func publishDecision(ctx context.Context, tx pgx.Tx, topic, jobID, merchantID, reason string) error {
	env, err := events.New(topic, jobID, events.MerchantDecisionData{
		JobID: jobID, MerchantID: merchantID, Reason: reason,
	})
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}
