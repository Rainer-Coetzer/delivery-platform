// Package consumers keeps the admin read model (jobs_projection) current
// from job, merchant-adjacent and delivery events.
package consumers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/reporting/internal/store"
)

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
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			return store.UpsertCreated(ctx, tx, data.JobID, data.MerchantID, data.MerchantName, data.CustomerID, data.TotalCents, env.Timestamp)
		})
	case events.JobStatusChanged:
		data, err := events.Decode[events.JobStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			var completedAt *time.Time
			if data.ToMilestone == lifecycle.Completed {
				t := env.Timestamp
				completedAt = &t
			}
			return store.UpdateStatus(ctx, tx, data.JobID, data.To, completedAt)
		})
	case events.DispatchAssigned:
		data, err := events.Decode[events.AssignedData](env)
		if err != nil {
			return err
		}
		return h.withDedup(ctx, env, func(tx pgx.Tx) error {
			return store.SetAgent(ctx, tx, data.JobID, data.AgentID)
		})
	default:
		return nil
	}
}

func (h *Handlers) withDedup(ctx context.Context, env events.Envelope, apply func(tx pgx.Tx) error) error {
	return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
		isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
		if err != nil || !isNew {
			return err
		}
		return apply(tx)
	})
}
