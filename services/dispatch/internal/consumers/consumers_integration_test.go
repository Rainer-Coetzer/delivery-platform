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
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
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

func availableAgent(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	d := &store.Agent{ID: uuid.NewString(), Name: "Test Agent", Vehicle: "bike", Status: "OFFLINE", Lat: -22.57, Lng: 17.08}
	if err := st.RegisterAgent(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentStatus(ctx, d.ID, "AVAILABLE"); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func envelope(t *testing.T, topic, jobID string, data any) events.Envelope {
	t.Helper()
	env, err := events.New(topic, jobID, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func ready(t *testing.T, jobID string) events.Envelope {
	return envelope(t, events.MerchantReady, jobID, events.MerchantReadyData{
		JobID: jobID, MerchantID: "r1", Pickup: pickup, Dropoff: dropoff,
	})
}

func cancelled(t *testing.T, jobID string) events.Envelope {
	return envelope(t, events.JobCancelled, jobID, events.JobCancelledData{JobID: jobID, Reason: "test"})
}

func agentStatus(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	d, err := st.GetAgent(context.Background(), id)
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

func TestCancelAfterAssignment_ReleasesAgent_Idempotent(t *testing.T) {
	h, _, st := setup(t)
	ctx := context.Background()
	agentID := availableAgent(t, st)
	jobID := uuid.NewString()

	if err := h.Handle(ctx, ready(t, jobID)); err != nil {
		t.Fatal(err)
	}
	if got := agentStatus(t, st, agentID); got != "BUSY" {
		t.Fatalf("after assignment agent is %s, want BUSY", got)
	}

	cancel := cancelled(t, jobID)
	for i := 0; i < 2; i++ { // the same event twice must have one effect
		if err := h.Handle(ctx, cancel); err != nil {
			t.Fatalf("cancel assignment %d: %v", i+1, err)
		}
	}
	if got := agentStatus(t, st, agentID); got != "AVAILABLE" {
		t.Fatalf("after cancel agent is %s, want AVAILABLE", got)
	}
	d, err := st.GetByJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "CANCELLED" {
		t.Fatalf("assignment is %s, want CANCELLED", d.Status)
	}
}

func TestCancelBeforeReady_NeverDispatches(t *testing.T) {
	h, _, st := setup(t)
	ctx := context.Background()
	agentID := availableAgent(t, st)
	jobID := uuid.NewString()

	// Different topics, so Kafka may deliver the cancel first.
	if err := h.Handle(ctx, cancelled(t, jobID)); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(ctx, ready(t, jobID)); err != nil {
		t.Fatal(err)
	}

	if _, err := st.GetByJob(ctx, jobID); err != store.ErrNotFound {
		t.Fatalf("expected no assignment for a cancelled job, got err=%v", err)
	}
	if got := agentStatus(t, st, agentID); got != "AVAILABLE" {
		t.Fatalf("agent is %s, want AVAILABLE", got)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending assignments = %d, want 0", n)
	}
}

func TestCancelWhilePending_DeletesPendingRow(t *testing.T) {
	h, _, st := setup(t) // no agents at all, so ready -> pending
	ctx := context.Background()
	jobID := uuid.NewString()

	if err := h.Handle(ctx, ready(t, jobID)); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, st); n != 1 {
		t.Fatalf("pending assignments = %d, want 1", n)
	}
	if err := h.Handle(ctx, cancelled(t, jobID)); err != nil {
		t.Fatal(err)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending assignments after cancel = %d, want 0", n)
	}
}
