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
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/delivery/internal/store"
)

var (
	pickup  = events.Location{Lat: -22.5608, Lng: 17.0658}
	dropoff = events.Location{Lat: -22.5594, Lng: 17.0832}
)

func setup(t *testing.T) (*Handlers, *db.Pool, *store.Store) {
	t.Helper()
	pool := dbtest.New(t, migrations.FS)
	return &Handlers{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, pool, store.New(pool)
}

func availableDriver(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	d := &store.Driver{ID: uuid.NewString(), Name: "Test Driver", Vehicle: "bike", Status: "OFFLINE", Lat: -22.57, Lng: 17.08}
	if err := st.RegisterDriver(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDriverStatus(ctx, d.ID, "AVAILABLE"); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func envelope(t *testing.T, topic, orderID string, data any) events.Envelope {
	t.Helper()
	env, err := events.New(topic, orderID, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func ready(t *testing.T, orderID string) events.Envelope {
	return envelope(t, events.RestaurantReady, orderID, events.RestaurantReadyData{
		OrderID: orderID, RestaurantID: "r1", Pickup: pickup, Dropoff: dropoff,
	})
}

func cancelled(t *testing.T, orderID string) events.Envelope {
	return envelope(t, events.OrderCancelled, orderID, events.OrderCancelledData{OrderID: orderID, Reason: "test"})
}

func driverStatus(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	d, err := st.GetDriver(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Status
}

func pendingCount(t *testing.T, st *store.Store) int {
	t.Helper()
	p, err := st.ListPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(p)
}

func TestCancelAfterAssignment_ReleasesDriver_Idempotent(t *testing.T) {
	h, _, st := setup(t)
	ctx := context.Background()
	driverID := availableDriver(t, st)
	orderID := uuid.NewString()

	if err := h.Handle(ctx, ready(t, orderID)); err != nil {
		t.Fatal(err)
	}
	if got := driverStatus(t, st, driverID); got != "BUSY" {
		t.Fatalf("after assignment driver is %s, want BUSY", got)
	}

	cancel := cancelled(t, orderID)
	for i := 0; i < 2; i++ { // the same event twice must have one effect
		if err := h.Handle(ctx, cancel); err != nil {
			t.Fatalf("cancel delivery %d: %v", i+1, err)
		}
	}
	if got := driverStatus(t, st, driverID); got != "AVAILABLE" {
		t.Fatalf("after cancel driver is %s, want AVAILABLE", got)
	}
	d, err := st.GetByOrder(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "CANCELLED" {
		t.Fatalf("delivery is %s, want CANCELLED", d.Status)
	}
}

func TestCancelBeforeReady_NeverDispatches(t *testing.T) {
	h, _, st := setup(t)
	ctx := context.Background()
	driverID := availableDriver(t, st)
	orderID := uuid.NewString()

	// Different topics, so Kafka may deliver the cancel first.
	if err := h.Handle(ctx, cancelled(t, orderID)); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(ctx, ready(t, orderID)); err != nil {
		t.Fatal(err)
	}

	if _, err := st.GetByOrder(ctx, orderID); err != store.ErrNotFound {
		t.Fatalf("expected no delivery for a cancelled order, got err=%v", err)
	}
	if got := driverStatus(t, st, driverID); got != "AVAILABLE" {
		t.Fatalf("driver is %s, want AVAILABLE", got)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending assignments = %d, want 0", n)
	}
}

func TestCancelWhilePending_DeletesPendingRow(t *testing.T) {
	h, _, st := setup(t) // no drivers at all, so ready -> pending
	ctx := context.Background()
	orderID := uuid.NewString()

	if err := h.Handle(ctx, ready(t, orderID)); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, st); n != 1 {
		t.Fatalf("pending assignments = %d, want 1", n)
	}
	if err := h.Handle(ctx, cancelled(t, orderID)); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending assignments after cancel = %d, want 0", n)
	}
}
