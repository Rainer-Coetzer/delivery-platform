//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	dispatchstrategy "github.com/rainercoetzer/delivery-platform/pkg/strategy/dispatch"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/eta"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

var (
	pickup  = events.Location{Lat: -22.5608, Lng: 17.0658}
	dropoff = events.Location{Lat: -22.5594, Lng: 17.0832}
)

func setup(t *testing.T) (*Handlers, *db.Pool, *store.Store) {
	return setupWith(t, "nearest", "")
}

func setupWith(t *testing.T, strategy, params string) (*Handlers, *db.Pool, *store.Store) {
	t.Helper()
	pool := dbtest.New(t, migrations.FS)
	pick, err := dispatchstrategy.Build(strategy, strategytest.Params(t, "dispatch", params))
	if err != nil {
		t.Fatal(err)
	}
	est, err := eta.Build("vehicle_speed", strategytest.Params(t, "eta", "{default_mps: 8.3}"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Dispatcher: &Dispatcher{Strategy: pick, ETA: est}}
	return h, pool, store.New(pool)
}

func availableAgent(t *testing.T, st *store.Store) string {
	return availableAgentAt(t, st, -22.57, 17.08)
}

func availableAgentAt(t *testing.T, st *store.Store, lat, lng float64) string {
	t.Helper()
	ctx := context.Background()
	d := &store.Agent{ID: uuid.NewString(), Name: "Test Agent", Vehicle: "bike", Status: "OFFLINE", Lat: lat, Lng: lng}
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

// ready is the jobs module announcing the step into ready_for_dispatch.
// Stop numbers 3 and 5 (not 0 and 1) prove dispatch reports back the
// job's own numbering rather than assuming a two-stop job.
func ready(t *testing.T, jobID string) events.Envelope {
	return envelope(t, events.JobStatusChanged, jobID, events.JobStatusChangedData{
		JobID: jobID, MerchantID: "r1", From: "PREPARING", To: "READY", ToMilestone: lifecycle.ReadyForDispatch,
		Stops: []events.Stop{
			{Seq: 3, Kind: events.StopPickup, Location: pickup, MerchantID: "r1"},
			{Seq: 5, Kind: events.StopDropoff, Location: dropoff},
		},
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

func TestOnlyReadyForDispatchAssigns(t *testing.T) {
	h, _, st := setup(t)
	ctx := context.Background()
	availableAgent(t, st)
	jobID := uuid.NewString()
	other := envelope(t, events.JobStatusChanged, jobID, events.JobStatusChangedData{
		JobID: jobID, From: "CREATED", To: "CONFIRMED", ToMilestone: lifecycle.Confirmed,
	})
	if err := h.Handle(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetByJob(ctx, jobID); err != store.ErrNotFound {
		t.Fatalf("a confirmed job must not be dispatched, got err=%v", err)
	}
}

// Picking up and completing report the job's stop numbers back, which is
// what moves the job along in the jobs module.
func TestProgressReportsStopSeqs(t *testing.T) {
	h, pool, st := setup(t)
	ctx := context.Background()
	availableAgent(t, st)
	jobID := uuid.NewString()
	if err := h.Handle(ctx, ready(t, jobID)); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetByJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := progress.Advance(ctx, pool, a.ID, progress.StatusAssigned, progress.StatusPickedUp); err != nil {
		t.Fatal(err)
	}
	if err := progress.Advance(ctx, pool, a.ID, progress.StatusPickedUp, progress.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT payload FROM outbox WHERE event_type = $1 ORDER BY id`, events.DispatchStopCompleted)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seqs []int
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var env events.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		d, err := events.Decode[events.StopCompletedData](env)
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, d.StopSeq)
	}
	if len(seqs) != 2 || seqs[0] != 3 || seqs[1] != 5 {
		t.Fatalf("stop-completed seqs = %v, want [3 5]", seqs)
	}
}

// least_loaded against the real candidate query: the agent right at the
// pickup already did a job today, so the idle one further away (but in
// range) gets the next one.
func TestLeastLoadedUsesTodaysAssignments(t *testing.T) {
	h, pool, st := setupWith(t, "least_loaded", "{within_km: 5}")
	ctx := context.Background()
	near := availableAgentAt(t, st, pickup.Lat, pickup.Lng)
	first := uuid.NewString()
	if err := h.Handle(ctx, ready(t, first)); err != nil {
		t.Fatal(err)
	}
	if a, err := st.GetByJob(ctx, first); err != nil || a.AgentID != near {
		t.Fatalf("the only agent takes the first job: %+v %v", a, err)
	}
	a, _ := st.GetByJob(ctx, first)
	if err := progress.Advance(ctx, pool, a.ID, progress.StatusAssigned, progress.StatusPickedUp); err != nil {
		t.Fatal(err)
	}
	if err := progress.Advance(ctx, pool, a.ID, progress.StatusPickedUp, progress.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	far := availableAgentAt(t, st, pickup.Lat+0.02, pickup.Lng) // ~2 km away, no jobs yet
	second := uuid.NewString()
	if err := h.Handle(ctx, ready(t, second)); err != nil {
		t.Fatal(err)
	}
	if a, err := st.GetByJob(ctx, second); err != nil || a.AgentID != far {
		t.Fatalf("least_loaded should pick the idle agent %s, got %+v %v", far, a, err)
	}
}
