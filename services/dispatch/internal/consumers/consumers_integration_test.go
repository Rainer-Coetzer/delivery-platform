//go:build integration

package consumers

import (
	"context"
	"encoding/json"
	"errors"
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
	if err := progress.CompleteStop(ctx, pool, a.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := progress.CompleteStop(ctx, pool, a.ID, 5); err != nil {
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
	if err := progress.CompleteStop(ctx, pool, a.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := progress.CompleteStop(ctx, pool, a.ID, 5); err != nil {
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

func vehicle(t *testing.T, st *store.Store, lat, lng float64, grams, cm3 int64, caps ...string) string {
	t.Helper()
	ctx := context.Background()
	d := &store.Agent{ID: uuid.NewString(), Name: "Vehicle", Vehicle: "van", Status: "OFFLINE", Lat: lat, Lng: lng,
		CapacityGrams: grams, CapacityCm3: cm3, Capabilities: caps}
	if err := st.RegisterAgent(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentStatus(ctx, d.ID, "AVAILABLE"); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func sized(t *testing.T, jobID string, grams, cm3 int64, requires ...string) events.Envelope {
	env := ready(t, jobID)
	var d events.JobStatusChangedData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		t.Fatal(err)
	}
	d.Size = &events.JobSize{WeightGrams: grams, VolumeCm3: cm3}
	d.Requires = requires
	return envelope(t, events.JobStatusChanged, jobID, d)
}

// With the capacity module on, only a vehicle that carries the load and
// has every required capability is a candidate, however near the others.
func TestCapacityFiltersCandidates(t *testing.T) {
	h, _, st := setupWith(t, "nearest", "")
	h.Dispatcher.Capacity = true
	ctx := context.Background()
	availableAgentAt(t, st, pickup.Lat, pickup.Lng)                                      // right there, capacity not declared
	vehicle(t, st, pickup.Lat, pickup.Lng, 15_000, 60_000)                               // bike: too small
	van := vehicle(t, st, pickup.Lat+0.01, pickup.Lng, 800_000, 6_000_000, "cold_chain") // further, fits

	cold := uuid.NewString()
	if err := h.Handle(ctx, sized(t, cold, 100_000, 500_000, "cold_chain")); err != nil {
		t.Fatal(err)
	}
	if a, err := st.GetByJob(ctx, cold); err != nil || a.AgentID != van {
		t.Fatalf("the cold van should get it: %+v %v", a, err)
	}

	hazmat := uuid.NewString()
	if err := h.Handle(ctx, sized(t, hazmat, 1_000, 1_000, "hazmat")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetByJob(ctx, hazmat); err != store.ErrNotFound {
		t.Fatalf("nobody can carry hazmat: the job must wait, got %v", err)
	}
	pending, err := st.ListPending(ctx)
	if err != nil || len(pending) != 1 || pending[0].WeightGrams != 1_000 || len(pending[0].Requires) != 1 {
		t.Fatalf("the waiting job keeps its size and needs for the retry: %+v %v", pending, err)
	}
}

// A multi-drop route: the agent works through the stops in order, the
// assignment stays underway until the last, and every stop is reported.
func TestMultiStopRouteRunsInOrder(t *testing.T) {
	h, pool, st := setup(t)
	ctx := context.Background()
	agentID := availableAgent(t, st)
	jobID := uuid.NewString()
	env := envelope(t, events.JobStatusChanged, jobID, events.JobStatusChangedData{
		JobID: jobID, To: "READY", ToMilestone: lifecycle.ReadyForDispatch,
		Stops: []events.Stop{
			{Seq: 0, Kind: events.StopPickup, Location: pickup},
			{Seq: 1, Kind: events.StopDropoff, Location: dropoff},
			{Seq: 2, Kind: events.StopDropoff, Location: events.Location{Lat: dropoff.Lat + 0.01, Lng: dropoff.Lng}},
		},
	})
	if err := h.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetByJob(ctx, jobID)
	if err != nil || a.Next == nil || a.Next.Seq != 0 || a.DropoffSeq != 2 {
		t.Fatalf("assignment should start at stop 0 and end at 2: %+v %v", a, err)
	}

	step := func(seq int, wantErr bool, wantStatus string) {
		t.Helper()
		err := progress.CompleteStop(ctx, pool, a.ID, seq)
		if wantErr != (err != nil) {
			t.Fatalf("stop %d: err = %v, want error %v", seq, err, wantErr)
		}
		if wantErr && !errors.Is(err, progress.ErrWrongState) {
			t.Fatalf("stop %d: out of order must be ErrWrongState, got %v", seq, err)
		}
		got, err := st.GetByJob(ctx, jobID)
		if err != nil || got.Status != wantStatus {
			t.Fatalf("after stop %d: status %s, want %s (%v)", seq, got.Status, wantStatus, err)
		}
	}
	step(1, true, "ASSIGNED") // can't drop before picking up
	step(0, false, "PICKED_UP")
	step(2, true, "PICKED_UP") // can't skip a drop
	step(1, false, "PICKED_UP")
	if got := agentStatus(t, st, agentID); got != "BUSY" {
		t.Fatalf("mid-route the agent is %s, want BUSY", got)
	}
	step(2, false, "COMPLETED")
	if got := agentStatus(t, st, agentID); got != "AVAILABLE" {
		t.Fatalf("after the last stop the agent is %s, want AVAILABLE", got)
	}
	step(2, true, "COMPLETED") // done is done

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type = $1`, events.DispatchStopCompleted).Scan(&n); err != nil || n != 3 {
		t.Fatalf("one stop-completed per stop: %d %v", n, err)
	}
}
