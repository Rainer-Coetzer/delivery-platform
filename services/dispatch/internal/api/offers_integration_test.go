//go:build integration

package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	dispatchstrategy "github.com/rainercoetzer/delivery-platform/pkg/strategy/dispatch"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/eta"
	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

type offerWorld struct {
	pool       *db.Pool
	st         *store.Store
	dispatcher *consumers.Dispatcher
	handlers   *consumers.Handlers
}

func newWorld(t *testing.T, strategy, params string) *offerWorld {
	t.Helper()
	pool := dbtest.New(t, migrations.FS)
	s, err := dispatchstrategy.Build(strategy, strategytest.Params(t, "dispatch", params))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eta.Build("vehicle_speed", strategytest.Params(t, "eta", "{default_mps: 8}"))
	if err != nil {
		t.Fatal(err)
	}
	d := &consumers.Dispatcher{Strategy: s, ETA: e}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &offerWorld{pool: pool, st: store.New(pool), dispatcher: d, handlers: &consumers.Handlers{Pool: pool, Log: log, Dispatcher: d}}
}

func (w *offerWorld) agentAt(t *testing.T, id string, lat float64) {
	t.Helper()
	ctx := context.Background()
	if err := w.st.RegisterAgent(ctx, &store.Agent{ID: id, Name: id, Vehicle: "bike", Status: "OFFLINE", Lat: lat, Lng: 17.08}); err != nil {
		t.Fatal(err)
	}
	if err := w.st.SetAgentStatus(ctx, id, "AVAILABLE"); err != nil {
		t.Fatal(err)
	}
}

func (w *offerWorld) ready(t *testing.T) string {
	t.Helper()
	jobID := uuid.NewString()
	env, err := events.New(events.JobStatusChanged, jobID, events.JobStatusChangedData{
		JobID: jobID, To: "READY", ToMilestone: lifecycle.ReadyForDispatch,
		Stops: []events.Stop{
			{Seq: 0, Kind: "pickup", Location: events.Location{Lat: -22.570, Lng: 17.08}},
			{Seq: 1, Kind: "dropoff", Location: events.Location{Lat: -22.52, Lng: 17.06}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.handlers.Handle(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func (w *offerWorld) call(t *testing.T, c *auth.Claims, method, path, body string) (int, string) {
	t.Helper()
	a := &API{Pool: w.pool, Store: w.st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c), Dispatcher: w.dispatcher}
	r := chi.NewRouter()
	a.Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

func agentClaims(id string) *auth.Claims {
	return &auth.Claims{UserID: "u-" + id, Role: auth.RoleAgent, RefID: id}
}

// The nearest two are asked; the third isn't; the first to accept wins.
func TestOpenOffer(t *testing.T) {
	w := newWorld(t, "open_offer", "{offer_to: 2, expires_seconds: 60, within_km: 10}")
	w.agentAt(t, "near", -22.571)
	w.agentAt(t, "mid", -22.575)
	w.agentAt(t, "far", -22.600)
	jobID := w.ready(t)

	if _, err := w.st.GetByJob(context.Background(), jobID); err != store.ErrNotFound {
		t.Fatalf("an offered job isn't assigned until someone accepts: %v", err)
	}
	if due, _ := w.st.ListPending(context.Background()); len(due) != 0 {
		t.Fatalf("an open offer holds the job back from the retrier: %+v", due)
	}
	if c, b := w.call(t, agentClaims("mid"), "GET", "/agents/mid/offers", ""); c != 200 || !strings.Contains(b, jobID) {
		t.Fatalf("mid sees the offer: %d %s", c, b)
	}
	if c, b := w.call(t, agentClaims("far"), "POST", "/offers/"+jobID+"/accept", ""); c != 409 {
		t.Fatalf("far wasn't asked: %d %s", c, b)
	}
	if c, b := w.call(t, agentClaims("mid"), "POST", "/offers/"+jobID+"/accept", ""); c != 200 {
		t.Fatalf("mid accepts first: %d %s", c, b)
	}
	if a, err := w.st.GetByJob(context.Background(), jobID); err != nil || a.AgentID != "mid" {
		t.Fatalf("mid has the job: %+v %v", a, err)
	}
	if c, b := w.call(t, agentClaims("near"), "POST", "/offers/"+jobID+"/accept", ""); c != 409 {
		t.Fatalf("too late for near: %d %s", c, b)
	}
	if c, b := w.call(t, agentClaims("near"), "GET", "/agents/near/offers", ""); c != 200 || strings.Contains(b, jobID) {
		t.Fatalf("the taken offer is gone from near's list: %d %s", c, b)
	}
}

// Under manual, nothing is assigned until an admin says who.
func TestManualAssign(t *testing.T) {
	w := newWorld(t, "manual", "")
	w.agentAt(t, "a1", -22.571)
	w.agentAt(t, "a2", -22.575)
	admin := &auth.Claims{UserID: "x", Role: auth.RoleAdmin}
	jobID := w.ready(t)

	if c, b := w.call(t, admin, "GET", "/assignments/queue", ""); c != 200 || !strings.Contains(b, jobID) {
		t.Fatalf("the job waits in the queue: %d %s", c, b)
	}
	if c, b := w.call(t, admin, "POST", "/assignments", `{"jobId":"`+jobID+`","agentId":"a2"}`); c != 200 {
		t.Fatalf("admin assigns a2: %d %s", c, b)
	}
	if a, err := w.st.GetByJob(context.Background(), jobID); err != nil || a.AgentID != "a2" {
		t.Fatalf("a2 has it: %+v %v", a, err)
	}
	if c, b := w.call(t, admin, "POST", "/assignments", `{"jobId":"`+jobID+`","agentId":"a1"}`); c != 409 {
		t.Fatalf("assigning it twice: %d %s", c, b)
	}
	other := w.ready(t)
	if c, b := w.call(t, admin, "POST", "/assignments", `{"jobId":"`+other+`","agentId":"a2"}`); c != 409 || !strings.Contains(b, "isn't available") {
		t.Fatalf("a2 is busy now: %d %s", c, b)
	}
}

// With the capacity module on, an admin can't hand a load to a vehicle
// that can't carry it: the same rule automatic dispatch applies.
func TestManualAssignRespectsCapacity(t *testing.T) {
	w := newWorld(t, "manual", "")
	w.dispatcher.Capacity = true
	ctx := context.Background()
	for _, a := range []*store.Agent{
		{ID: "bike", Name: "bike", Vehicle: "bike", Status: "OFFLINE", Lat: -22.571, Lng: 17.08, CapacityGrams: 15000, CapacityCm3: 60000},
		{ID: "truck", Name: "truck", Vehicle: "truck", Status: "OFFLINE", Lat: -22.575, Lng: 17.08, CapacityGrams: 8000000, CapacityCm3: 40000000},
	} {
		if err := w.st.RegisterAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := w.st.SetAgentStatus(ctx, a.ID, "AVAILABLE"); err != nil {
			t.Fatal(err)
		}
	}
	jobID := uuid.NewString()
	env, _ := events.New(events.JobStatusChanged, jobID, events.JobStatusChangedData{
		JobID: jobID, To: "READY", ToMilestone: lifecycle.ReadyForDispatch, Size: &events.JobSize{WeightGrams: 500000, VolumeCm3: 2000000},
		Stops: []events.Stop{{Seq: 0, Kind: "pickup", Location: events.Location{Lat: -22.57, Lng: 17.08}}, {Seq: 1, Kind: "dropoff", Location: events.Location{Lat: -22.52, Lng: 17.06}}},
	})
	if err := w.handlers.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	admin := &auth.Claims{UserID: "x", Role: auth.RoleAdmin}
	if c, b := w.call(t, admin, "POST", "/assignments", `{"jobId":"`+jobID+`","agentId":"bike"}`); c != 409 || !strings.Contains(b, "doesn't fit") {
		t.Fatalf("half a ton on a bike: %d %s", c, b)
	}
	if c, b := w.call(t, admin, "POST", "/assignments", `{"jobId":"`+jobID+`","agentId":"truck"}`); c != 200 {
		t.Fatalf("the truck takes it: %d %s", c, b)
	}
}
