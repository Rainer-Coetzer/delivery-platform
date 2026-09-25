//go:build integration

package consumers

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/store"
)

// setup gives a merchant open around the clock (00:00-00:00 spans
// midnight) with one catalogue item, 5 in stock.
func setup(t *testing.T) (*Handlers, *db.Pool, *store.Store, string, string) {
	t.Helper()
	pool := dbtest.New(t, migrations.FS)
	st := store.New(pool, "default")
	ctx := context.Background()
	m := &domain.Merchant{ID: uuid.NewString(), Name: "Kapana", Lat: -22.56, Lng: 17.07, Timezone: "Africa/Windhoek", IsOpen: true}
	if err := st.CreateMerchant(ctx, m); err != nil {
		t.Fatal(err)
	}
	item := &domain.CatalogueItem{ID: uuid.NewString(), MerchantID: m.ID, Name: "Platter", PriceCents: 6500, StockQty: 5, Available: true}
	if err := st.CreateCatalogueItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	return &Handlers{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, pool, st, m.ID, item.ID
}

func handle(t *testing.T, h *Handlers, topic, jobID string, data any) {
	t.Helper()
	env, err := events.New(topic, jobID, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(context.Background(), env); err != nil {
		t.Fatalf("%s: %v", topic, err)
	}
}

func stock(t *testing.T, st *store.Store, merchantID string) int {
	t.Helper()
	items, err := st.ListCatalogueItems(context.Background(), merchantID)
	if err != nil || len(items) != 1 {
		t.Fatalf("list items: %v %d", err, len(items))
	}
	return items[0].StockQty
}

func status(t *testing.T, st *store.Store, jobID string) string {
	t.Helper()
	mj, err := st.GetMerchantJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return mj.Status
}

func outboxTopics(t *testing.T, pool *db.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestAcceptPrepareCancel(t *testing.T) {
	h, pool, st, merchantID, itemID := setup(t)
	jobID := uuid.NewString()
	lines := []events.JobLine{{CatalogueItemID: itemID, Name: "Platter", Quantity: 2, UnitPriceCents: 6500}}

	handle(t, h, events.JobCreated, jobID, events.JobCreatedData{JobID: jobID, MerchantID: merchantID, Items: lines})
	if got := status(t, st, jobID); got != store.StatusAccepted {
		t.Fatalf("after jobs.created: %s, want %s", got, store.StatusAccepted)
	}
	if got := stock(t, st, merchantID); got != 3 {
		t.Fatalf("stock after accepting 2 of 5 = %d", got)
	}
	if got := outboxTopics(t, pool); len(got) != 1 || got[0] != events.MerchantAccepted {
		t.Fatalf("outbox = %v", got)
	}

	// Prep starts on the confirmed milestone, whatever the pack calls it.
	handle(t, h, events.JobStatusChanged, jobID, events.JobStatusChangedData{JobID: jobID, To: "PREPARING"})
	if got := status(t, st, jobID); got != store.StatusAccepted {
		t.Fatalf("a step with no milestone must not start prep, got %s", got)
	}
	handle(t, h, events.JobStatusChanged, jobID, events.JobStatusChangedData{JobID: jobID, MerchantID: merchantID, To: "WHATEVER", ToMilestone: lifecycle.Confirmed})
	if got := status(t, st, jobID); got != store.StatusPreparing {
		t.Fatalf("after confirmed: %s, want %s", got, store.StatusPreparing)
	}

	handle(t, h, events.JobCancelled, jobID, events.JobCancelledData{JobID: jobID, MerchantID: merchantID, Items: lines})
	if got := stock(t, st, merchantID); got != 5 {
		t.Fatalf("stock after cancel = %d, want it all back", got)
	}
	if got := status(t, st, jobID); got != store.StatusCancelled {
		t.Fatalf("after cancel: %s", got)
	}
}

func TestJobWithoutMerchantIsNotOurs(t *testing.T) {
	h, pool, _, _, _ := setup(t)
	jobID := uuid.NewString()
	handle(t, h, events.JobCreated, jobID, events.JobCreatedData{JobID: jobID})
	if got := outboxTopics(t, pool); len(got) != 0 {
		t.Fatalf("a courier job must get no merchant decision, got %v", got)
	}
}

// A job with no merchant reaching confirmed (freight) is none of the
// merchant module's business: no prep, and no warning either.
func TestConfirmedWithoutMerchantIsIgnored(t *testing.T) {
	h, pool, _, _, _ := setup(t)
	jobID := uuid.NewString()
	handle(t, h, events.JobStatusChanged, jobID, events.JobStatusChangedData{JobID: jobID, To: "APPROVED", ToMilestone: lifecycle.Confirmed})
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM processed_events`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("it shouldn't even be recorded as handled: %d %v", n, err)
	}
}
