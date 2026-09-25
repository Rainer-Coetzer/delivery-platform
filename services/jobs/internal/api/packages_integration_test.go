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

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

// A courier-shaped pack: the customer names both ends and declares the
// packages; the fee is per kilometre.
const parcelPack = `schema: 1
name: parcel
modules: [packages, capacity]
roles: [customer, agent, admin]
job: { stops: point_to_point, lines: packages, requires: [signature_pad] }
strategies:
  pricing: { name: per_km, params: { base_cents: 2000, per_km_cents: 800 } }
  dispatch: { name: capacity_aware }
  eta: { name: vehicle_speed, params: { default_mps: 8 } }
  payment: [ { name: cash } ]
terminology: { en: { job: delivery } }
settings:
  packages: { max_lines: 5, max_quantity: 10, max_weight_g: 30000, max_dimension_mm: 1500, max_declared_value_cents: 5000000 }
`

const parcelLifecycle = `schema: 1
initial: BOOKED
states:
  - { name: BOOKED, milestone: created }
  - { name: READY, milestone: ready_for_dispatch }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
transitions:
  - { from: BOOKED, to: READY, auto: true }
  - { from: READY, to: DONE, on: dispatch.stop-completed, stop: last_stop }
  - { from: "*", to: GONE, by: [admin] }
`

func TestPointToPointWithPackages(t *testing.T) {
	p, err := packs.Load(fstest.MapFS{
		"parcel/pack.yaml":      {Data: []byte(parcelPack)},
		"parcel/lifecycle.yaml": {Data: []byte(parcelLifecycle)},
	}, "parcel", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := app.BuildStrategies(p)
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth: as(&auth.Claims{UserID: "c1", Name: "Nangula", Role: auth.RoleCustomer}),
		Pack: p, Pricing: strategies.Pricing, Payments: strategies.Payments}
	r := chi.NewRouter()
	a.Routes(r)
	post := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("POST", "/jobs", strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}

	// CBD to Katutura, about 5.4 km: 2000 + 6 x 800.
	const route = `"paymentMethod": "cash",
		"pickup": {"lat": -22.5700, "lng": 17.0836, "label": "Office"},
		"dropoff": {"lat": -22.5250, "lng": 17.0600, "label": "Home"}`
	code, body := post(`{` + route + `, "packages": [
		{"description": "Documents", "quantity": 1, "weightGrams": 500, "lengthMm": 320, "widthMm": 230, "heightMm": 10},
		{"description": "Laptop", "quantity": 1, "weightGrams": 2500, "lengthMm": 400, "widthMm": 300, "heightMm": 80, "declaredValueCents": 1500000}]}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var j domain.Job
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		t.Fatal(err)
	}
	if j.MerchantID != "" || len(j.Stops) != 2 || j.Stops[0].Location.Label != "Office" || len(j.Items) != 2 {
		t.Fatalf("job shape: %+v", j)
	}
	if j.SubtotalCents != 0 || j.DeliveryFeeCents != 2000+6*800 || j.TotalCents != j.DeliveryFeeCents {
		t.Fatalf("price: subtotal %d fee %d total %d", j.SubtotalCents, j.DeliveryFeeCents, j.TotalCents)
	}
	// BOOKED settles straight to READY, and dispatch gets the size and needs.
	if j.Status != "READY" || j.Milestone != lifecycle.ReadyForDispatch {
		t.Fatalf("status %s/%s", j.Status, j.Milestone)
	}
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1`, events.JobStatusChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var env events.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	d, _ := events.Decode[events.JobStatusChangedData](env)
	if d.Size == nil || d.Size.WeightGrams != 3000 || d.Size.VolumeCm3 != 736+9600 || len(d.Requires) != 1 {
		t.Fatalf("ready_for_dispatch carries size and needs: %+v %v", d.Size, d.Requires)
	}
	stored, err := store.New(pool).Get(context.Background(), j.ID)
	if err != nil || stored.Items[1].DeclaredValueCents != 1500000 || stored.Items[1].Kind != domain.LinePackage {
		t.Fatalf("package lines persist: %+v %v", stored.Items, err)
	}

	for _, bad := range []string{
		`{` + route + `, "packages": []}`,
		`{` + route + `, "packages": [{"description": "Piano", "quantity": 1, "weightGrams": 300000, "lengthMm": 1500, "widthMm": 600, "heightMm": 1300}]}`,
		`{"paymentMethod": "cash", "dropoff": {"lat": -22.5, "lng": 17.0}, "packages": [{"description": "x", "quantity": 1, "weightGrams": 1, "lengthMm": 1, "widthMm": 1, "heightMm": 1}]}`,
	} {
		if code, body := post(bad); code != 400 {
			t.Fatalf("want 400 for %s, got %d %s", bad, code, body)
		}
	}
}

func TestMultiStopJob(t *testing.T) {
	pack := strings.NewReplacer(
		"name: parcel", "name: haul",
		"modules: [packages, capacity]", "modules: [packages, capacity, multi-stop]",
		"stops: point_to_point", "stops: multi_stop",
	).Replace(parcelPack) + "  multi-stop: { max_stops: 5 }\n"
	p, err := packs.Load(fstest.MapFS{
		"haul/pack.yaml":      {Data: []byte(pack)},
		"haul/lifecycle.yaml": {Data: []byte(parcelLifecycle)},
	}, "haul", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := app.BuildStrategies(p)
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth: as(&auth.Claims{UserID: "c1", Role: auth.RoleCustomer}), Pack: p, Pricing: strategies.Pricing, Payments: strategies.Payments}
	r := chi.NewRouter()
	a.Routes(r)
	post := func(body string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("POST", "/jobs", strings.NewReader(body)))
		return rec.Code, rec.Body.String()
	}
	const pkgs = `"packages": [{"description": "Pallet", "quantity": 2, "weightGrams": 25000, "lengthMm": 1200, "widthMm": 800, "heightMm": 1000}]`
	// CBD -> Katutura -> back to the CBD: just over 11 km (each way is a
	// little over 5.5 km), so 12 km billed: distance runs through every stop.
	code, body := post(`{"paymentMethod": "cash", ` + pkgs + `, "stops": [
		{"kind": "pickup", "location": {"lat": -22.5700, "lng": 17.0836}},
		{"kind": "dropoff", "location": {"lat": -22.5250, "lng": 17.0600}},
		{"kind": "return", "location": {"lat": -22.5700, "lng": 17.0836}}]}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var j domain.Job
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		t.Fatal(err)
	}
	if len(j.Stops) != 3 || j.Stops[2].Kind != "return" || j.DeliveryFeeCents != 2000+12*800 {
		t.Fatalf("route and fee: %d stops, fee %d", len(j.Stops), j.DeliveryFeeCents)
	}
	if code, body := post(`{"paymentMethod": "cash", ` + pkgs + `, "stops": [{"kind": "dropoff", "location": {"lat": -22.5, "lng": 17.0}}, {"kind": "dropoff", "location": {"lat": -22.6, "lng": 17.1}}]}`); code != 400 || !strings.Contains(body, "first stop must be a pickup") {
		t.Fatalf("a route that starts with a drop: %d %s", code, body)
	}
}
