// Package consumers projects jobs.created and jobs.status-changed into
// this service's job_history read model.
package consumers

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/store"
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
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			return store.UpsertJobCreated(ctx, tx, &store.JobHistoryEntry{
				JobID: data.JobID, CustomerID: data.CustomerID, MerchantName: data.MerchantName,
				Status: "CREATED", TotalCents: data.TotalCents,
			}, env.Timestamp)
		})
	case events.JobStatusChanged:
		data, err := events.Decode[events.JobStatusChangedData](env)
		if err != nil {
			return err
		}
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			return store.UpdateJobStatus(ctx, tx, data.JobID, data.To)
		})
	case events.JobCancelled:
		return h.Pool.WithTx(ctx, func(tx pgx.Tx) error {
			isNew, err := db.InsertIfNew(ctx, tx, env.EventID, env.EventType)
			if err != nil || !isNew {
				return err
			}
			data, err := events.Decode[events.JobCancelledData](env)
			if err != nil {
				return err
			}
			return store.UpdateJobStatus(ctx, tx, data.JobID, events.StatusCancelled)
		})
	default:
		return nil
	}
}
