//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

// A pharmacy-style pack: a job waits for the merchant to approve the
// prescription, then goes straight to dispatch.
const approvalPack = `schema: 1
name: pharmacy
modules: [catalogue, approval]
roles: [customer, merchant, agent, admin]
job: { stops: merchant_to_customer, lines: catalogue }
strategies:
  pricing: { name: flat, params: { fee_cents: 500 } }
  dispatch: { name: nearest }
  eta: { name: vehicle_speed, params: { default_mps: 8 } }
  payment: [ { name: cash } ]
terminology: { en: { job: order } }
`

const approvalLifecycle = `schema: 1
initial: CREATED
states:
  - { name: CREATED, milestone: created }
  - { name: READY, milestone: ready_for_dispatch }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
facts:
  approved: { by: [merchant, admin] }
transitions:
  - { from: CREATED, to: READY, auto: true, when: [approved] }
  - { from: READY, to: DONE, on: dispatch.stop-completed, stop: last_stop }
  - { from: "*", to: GONE, by: [admin] }
`

func TestApprovalSetsFactAndUnlocksDispatch(t *testing.T) {
	p, err := packs.Load(fstest.MapFS{
		"pharmacy/pack.yaml":      {Data: []byte(approvalPack)},
		"pharmacy/lifecycle.yaml": {Data: []byte(approvalLifecycle)},
	}, "pharmacy", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	id := storedJob(t, pool, "CREATED", lifecycle.Created)

	post := func(c *auth.Claims, fact string) (int, domain.Job) {
		a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c), Pack: p}
		r := chi.NewRouter()
		a.Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("POST", "/jobs/"+id+"/facts/"+fact, strings.NewReader("")))
		var j domain.Job
		_ = json.Unmarshal(rec.Body.Bytes(), &j)
		return rec.Code, j
	}
	customer := &auth.Claims{UserID: "c1", Role: auth.RoleCustomer}
	otherMerchant := &auth.Claims{UserID: "o2", Role: auth.RoleMerchant, RefID: "m2"}
	merchant := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "m1"}

	if code, _ := post(customer, "approved"); code != 409 {
		t.Fatalf("a customer can't approve their own prescription: %d", code)
	}
	if code, _ := post(otherMerchant, "approved"); code != 403 {
		t.Fatalf("another merchant can't see the job: %d", code)
	}
	if code, _ := post(merchant, "teleported"); code != 409 {
		t.Fatalf("an unknown fact: %d", code)
	}
	code, j := post(merchant, "approved")
	if code != 200 || j.Status != "READY" || j.Milestone != lifecycle.ReadyForDispatch || !j.Facts["approved"] {
		t.Fatalf("approval should unlock READY: %d %+v", code, j)
	}
	if code, _ := post(merchant, "approved"); code != 409 {
		t.Fatalf("approving twice: %d", code)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE event_type = $1`, events.JobStatusChanged).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the auto step into READY must be published once: %d %v", n, err)
	}
}

// Food has no approval module: the endpoint doesn't exist there.
func TestFactEndpointNeedsTheApprovalModule(t *testing.T) {
	pool := dbtest.New(t, migrations.FS)
	id := storedJob(t, pool, "CREATED", lifecycle.Created)
	a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(&auth.Claims{Role: auth.RoleAdmin}), Pack: foodPack(t)}
	r := chi.NewRouter()
	a.Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/jobs/"+id+"/facts/paid", strings.NewReader("")))
	if rec.Code != 404 {
		t.Fatalf("got %d, want 404: an admin must not be able to mark food paid", rec.Code)
	}
}
