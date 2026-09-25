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
	"github.com/jackc/pgx/v5"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

func setup(t *testing.T) (*Handlers, *db.Pool) {
	t.Helper()
	p, err := packs.Load(packfs.FS, "food", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	return &Handlers{Pool: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Lifecycle: p.Lifecycle}, pool
}

// newJob stores a fresh food job the way the API does.
func newJob(t *testing.T, pool *db.Pool) string {
	t.Helper()
	j := &domain.Job{
		ID: uuid.NewString(), Pack: "food", CustomerID: "c1", CustomerName: "C", MerchantID: "m1", MerchantName: "Kapana",
		Status: "CREATED", Milestone: lifecycle.Created, Facts: map[string]bool{}, PaymentMethod: "card",
		Items: []domain.Item{{Kind: domain.LineCatalogue, CatalogueItemID: "i1", Name: "Platter", Quantity: 2, UnitPriceCents: 6500}},
		Stops: []domain.Stop{
			{Stop: events.Stop{Seq: 0, Kind: events.StopPickup, MerchantID: "m1", Location: events.Location{Lat: -22.56, Lng: 17.07}}},
			{Stop: events.Stop{Seq: 1, Kind: events.StopDropoff, Location: events.Location{Lat: -22.57, Lng: 17.08}}},
		},
	}
	err := pool.WithTx(context.Background(), func(tx pgx.Tx) error { return store.Create(context.Background(), tx, j, "CREATED", nil) })
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

func send(t *testing.T, h *Handlers, topic, jobID string, data any) error {
	t.Helper()
	env, err := events.New(topic, jobID, data)
	if err != nil {
		t.Fatal(err)
	}
	return h.Handle(context.Background(), env)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, pool *db.Pool, id string) *domain.Job {
	t.Helper()
	j, err := store.New(pool).Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// published returns the data of every outbox event of topic, in order.
func published[T any](t *testing.T, pool *db.Pool, topic string) []T {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1 ORDER BY id`, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var raw []byte
		must(t, rows.Scan(&raw))
		var env events.Envelope
		must(t, json.Unmarshal(raw, &env))
		d, err := events.Decode[T](env)
		must(t, err)
		out = append(out, d)
	}
	return out
}

func TestFoodJobRunsThroughThePackLifecycle(t *testing.T) {
	h, pool := setup(t)
	id := newJob(t, pool)

	must(t, send(t, h, events.PaymentCompleted, id, events.PaymentData{JobID: id}))
	if j := load(t, pool, id); j.Status != "CREATED" || !j.Facts["paid"] {
		t.Fatalf("paid alone: %s %v", j.Status, j.Facts)
	}
	must(t, send(t, h, events.MerchantAccepted, id, events.MerchantDecisionData{JobID: id}))
	if j := load(t, pool, id); j.Status != "PREPARING" || j.Milestone != lifecycle.Confirmed {
		t.Fatalf("both facts in: %s/%s, want PREPARING/confirmed", j.Status, j.Milestone)
	}
	must(t, send(t, h, events.MerchantReady, id, events.MerchantReadyData{JobID: id}))
	must(t, send(t, h, events.DispatchAssigned, id, events.AssignedData{JobID: id, AgentID: "a1"}))
	must(t, send(t, h, events.DispatchStopCompleted, id, events.StopCompletedData{JobID: id, StopSeq: 0}))
	if j := load(t, pool, id); j.Status != "OUT_FOR_DELIVERY" || j.AgentID != "a1" || j.Stops[0].CompletedAt == nil {
		t.Fatalf("after pickup: %s agent=%q stop0=%v", j.Status, j.AgentID, j.Stops[0].CompletedAt)
	}
	must(t, send(t, h, events.DispatchStopCompleted, id, events.StopCompletedData{JobID: id, StopSeq: 1}))
	if j := load(t, pool, id); j.Status != "DELIVERED" || j.Milestone != lifecycle.Completed {
		t.Fatalf("after dropoff: %s/%s", j.Status, j.Milestone)
	}

	changes := published[events.JobStatusChangedData](t, pool, events.JobStatusChanged)
	var got []string
	for _, c := range changes {
		got = append(got, c.To+"/"+c.ToMilestone)
		if c.ToMilestone == lifecycle.ReadyForDispatch && len(c.Stops) != 2 {
			t.Fatalf("the step into ready_for_dispatch must carry the stops, got %+v", c.Stops)
		}
		if c.ToMilestone != lifecycle.ReadyForDispatch && len(c.Stops) != 0 {
			t.Fatalf("only ready_for_dispatch carries stops, %s had %d", c.To, len(c.Stops))
		}
	}
	want := []string{"CONFIRMED/confirmed", "PREPARING/", "READY/ready_for_dispatch", "OUT_FOR_DELIVERY/in_transit", "DELIVERED/completed"}
	if len(got) != len(want) {
		t.Fatalf("status changes %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status changes %v, want %v", got, want)
		}
	}

	// A failure landing after the job finished is a race, not a fault.
	must(t, send(t, h, events.PaymentFailed, id, events.PaymentData{JobID: id, Reason: "late"}))
	if j := load(t, pool, id); j.Status != "DELIVERED" {
		t.Fatalf("a late failure moved a finished job to %s", j.Status)
	}
}

func TestCancelPublishesLinesAndReason(t *testing.T) {
	h, pool := setup(t)
	id := newJob(t, pool)
	must(t, send(t, h, events.MerchantRejected, id, events.MerchantDecisionData{JobID: id, Reason: "out of stock"}))
	j := load(t, pool, id)
	if j.Status != "CANCELLED" || j.CancelReason != "out of stock" {
		t.Fatalf("rejection: %s %q", j.Status, j.CancelReason)
	}
	c := published[events.JobCancelledData](t, pool, events.JobCancelled)
	if len(c) != 1 || c[0].Reason != "out of stock" || len(c[0].Items) != 1 || c[0].Items[0].Quantity != 2 {
		t.Fatalf("jobs.cancelled = %+v", c)
	}
}

func TestDuplicateEventHasOneEffect(t *testing.T) {
	h, pool := setup(t)
	id := newJob(t, pool)
	env, err := events.New(events.DispatchAssigned, id, events.AssignedData{JobID: id, AgentID: "a1"})
	must(t, err)
	must(t, h.Handle(context.Background(), env))
	must(t, h.Handle(context.Background(), env))
	pay, err := events.New(events.PaymentCompleted, id, events.PaymentData{JobID: id})
	must(t, err)
	must(t, h.Handle(context.Background(), pay))
	must(t, h.Handle(context.Background(), pay))
	if n := len(published[events.JobStatusChangedData](t, pool, events.JobStatusChanged)); n != 0 {
		t.Fatalf("nothing should have moved, got %d status changes", n)
	}
}

// An event that makes no sense for a live job (ready before it was even
// confirmed) means the pack and the event flow disagree. It must fail,
// straight to the dead letters where someone sees it, not vanish.
func TestImpossibleEventOnLiveJobFails(t *testing.T) {
	h, pool := setup(t)
	id := newJob(t, pool)
	err := send(t, h, events.MerchantReady, id, events.MerchantReadyData{JobID: id})
	if !errors.Is(err, lifecycle.ErrNoTransition) || !bus.IsPermanent(err) {
		t.Fatalf("want a permanent ErrNoTransition (dead-letter now, don't retry), got %v", err)
	}
	if j := load(t, pool, id); j.Status != "CREATED" {
		t.Fatalf("job moved to %s", j.Status)
	}
}

// A drop in the middle of a multi-stop route matches no food transition;
// it is recorded as progress, not dead-lettered as a fault.
func TestMiddleStopIsRecordedNotRefused(t *testing.T) {
	h, pool := setup(t)
	id := newJob(t, pool)
	_, err := pool.Exec(context.Background(), `INSERT INTO job_stops (job_id, seq, kind, lat, lng) VALUES ($1, 2, 'dropoff', -22.58, 17.09)`, id)
	must(t, err)
	must(t, send(t, h, events.PaymentCompleted, id, events.PaymentData{JobID: id}))
	must(t, send(t, h, events.MerchantAccepted, id, events.MerchantDecisionData{JobID: id}))
	must(t, send(t, h, events.MerchantReady, id, events.MerchantReadyData{JobID: id}))
	must(t, send(t, h, events.DispatchStopCompleted, id, events.StopCompletedData{JobID: id, StopSeq: 0}))
	must(t, send(t, h, events.DispatchStopCompleted, id, events.StopCompletedData{JobID: id, StopSeq: 1}))
	j := load(t, pool, id)
	if j.Status != "OUT_FOR_DELIVERY" || j.Stops[1].CompletedAt == nil {
		t.Fatalf("middle stop: status %s, stop 1 done %v", j.Status, j.Stops[1].CompletedAt)
	}
	must(t, send(t, h, events.DispatchStopCompleted, id, events.StopCompletedData{JobID: id, StopSeq: 2}))
	if j := load(t, pool, id); j.Status != "DELIVERED" {
		t.Fatalf("the last stop delivers: %s", j.Status)
	}
}
