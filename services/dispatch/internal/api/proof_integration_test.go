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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/blob"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/progress"
	"github.com/rainercoetzer/delivery-platform/services/dispatch/internal/store"
)

// A two-stop job: a photo at the pickup, the customer's PIN at the drop.
func assignedJob(t *testing.T, pool *db.Pool) (assignmentID, jobID string) {
	t.Helper()
	ctx := context.Background()
	st := store.New(pool)
	agent := &store.Agent{ID: "a1", Name: "A", Vehicle: "bike", Status: "BUSY", Lat: -22.57, Lng: 17.08}
	if err := st.RegisterAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	jobID = uuid.NewString()
	d := &store.Assignment{ID: uuid.NewString(), JobID: jobID, AgentID: "a1", CustomerID: "c1", MerchantID: "m1",
		PickupLat: -22.57, PickupLng: 17.08, DropoffLat: -22.52, DropoffLng: 17.06, PickupSeq: 0, DropoffSeq: 1, Status: progress.StatusAssigned}
	stops := []events.Stop{
		{Seq: 0, Kind: "pickup", Location: events.Location{Lat: -22.57, Lng: 17.08}, Proof: "photo"},
		{Seq: 1, Kind: "dropoff", Location: events.Location{Lat: -22.52, Lng: 17.06}, Proof: "pin", PinHash: events.PinHash(jobID, 1, "0427")},
	}
	if err := pool.WithTx(ctx, func(tx pgx.Tx) error { return store.InsertAssignment(ctx, tx, d, stops) }); err != nil {
		t.Fatal(err)
	}
	return d.ID, jobID
}

func TestProofFlow(t *testing.T) {
	pool := dbtest.New(t, migrations.FS)
	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := assignedJob(t, pool)
	agent := &auth.Claims{UserID: "u-agent", Role: auth.RoleAgent, RefID: "a1"}
	customer := &auth.Claims{UserID: "c1", Role: auth.RoleCustomer}
	stranger := &auth.Claims{UserID: "c2", Role: auth.RoleCustomer}
	admin := &auth.Claims{UserID: "u-admin", Role: auth.RoleAdmin}

	do := func(c *auth.Claims, method, path, contentType, body string) (int, string) {
		t.Helper()
		a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c),
			Proof: &packs.ProofSettings{PinAttempts: 3, MaxPhotoBytes: 64}, Blobs: blobs}
		r := chi.NewRouter()
		a.Routes(r)
		req := httptest.NewRequest(method, "/assignments/"+id+path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	expect := func(what string, code, want int, body string) {
		t.Helper()
		if code != want {
			t.Fatalf("%s: %d, want %d (%s)", what, code, want, body)
		}
	}

	// Pickup: a photo.
	c, b := do(agent, "POST", "/stops/0/completed", "", "")
	expect("complete without the photo", c, 409, b)
	c, b = do(agent, "POST", "/stops/0/proof", "text/plain", "not an image")
	expect("wrong type", c, 415, b)
	c, b = do(agent, "POST", "/stops/0/proof", "image/png", strings.Repeat("x", 65))
	expect("too big", c, 413, b)
	c, b = do(agent, "POST", "/stops/1/proof", "application/json", `{"pin":"0427"}`)
	expect("proof for a stop that isn't next", c, 409, b)
	c, b = do(agent, "POST", "/stops/0/proof", "image/png", "png bytes")
	expect("photo", c, 201, b)
	c, b = do(agent, "POST", "/stops/0/proof", "image/png", "png bytes")
	expect("a second photo", c, 409, b)
	c, b = do(agent, "POST", "/stops/0/completed", "", "")
	expect("complete the pickup", c, 200, b)
	c, b = do(customer, "GET", "/stops/0/proof", "", "")
	if c != 200 || b != "png bytes" {
		t.Fatalf("the customer sees the photo: %d %q", c, b)
	}
	c, b = do(stranger, "GET", "/stops/0/proof", "", "")
	expect("someone else's proof", c, 404, b)

	// Drop: the PIN, locked after 3 wrong tries, then an admin override.
	c, b = do(agent, "POST", "/stops/1/proof", "application/json", `{"pin":"1111"}`)
	expect("wrong PIN", c, 422, b)
	if !strings.Contains(b, "2 tries left") {
		t.Fatalf("say how many tries are left: %s", b)
	}
	do(agent, "POST", "/stops/1/proof", "application/json", `{"pin":"2222"}`)
	c, b = do(agent, "POST", "/stops/1/proof", "application/json", `{"pin":"3333"}`)
	expect("third wrong PIN locks", c, 423, b)
	c, b = do(agent, "POST", "/stops/1/proof", "application/json", `{"pin":"0427"}`)
	expect("even the right PIN, once locked", c, 423, b)
	c, b = do(admin, "POST", "/stops/1/proof", "application/json", `{"override":""}`)
	expect("an override needs a reason", c, 400, b)
	c, b = do(admin, "POST", "/stops/1/proof", "application/json", `{"override":"customer phoned, ID seen on video call"}`)
	expect("admin override", c, 201, b)
	c, b = do(agent, "POST", "/stops/1/completed", "", "")
	expect("complete the drop", c, 200, b)
	c, b = do(customer, "GET", "/stops/1/proof", "", "")
	if c != 200 || !strings.Contains(b, `"kind":"admin_override"`) || strings.Contains(b, "0427") {
		t.Fatalf("the record shows the override and never the PIN: %d %s", c, b)
	}

	// The jobs module hears what proof each stop got.
	rows, err := pool.Query(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1 ORDER BY id`, events.DispatchStopCompleted)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var raw []byte
		var env events.Envelope
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(raw, &env)
		d, _ := events.Decode[events.StopCompletedData](env)
		if d.Proof == nil {
			t.Fatalf("stop %d completed without a proof ref", d.StopSeq)
		}
		kinds = append(kinds, d.Proof.Kind)
	}
	if strings.Join(kinds, ",") != "photo,admin_override" {
		t.Fatalf("proof refs = %v", kinds)
	}
}

func TestRightPINAndIDCheck(t *testing.T) {
	pool := dbtest.New(t, migrations.FS)
	id, _ := assignedJob(t, pool)
	agent := &auth.Claims{UserID: "u-agent", Role: auth.RoleAgent, RefID: "a1"}
	a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(agent),
		Proof: &packs.ProofSettings{PinAttempts: 3, MaxPhotoBytes: 64}}
	r := chi.NewRouter()
	a.Routes(r)
	post := func(path, ct, body string) int {
		req := httptest.NewRequest("POST", "/assignments/"+id+path, strings.NewReader(body))
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	// Skip the pickup's photo with a direct proof row: this test is about the PIN.
	ctx := context.Background()
	if err := pool.WithTx(ctx, func(tx pgx.Tx) error {
		return store.InsertProof(ctx, tx, &store.Proof{ID: uuid.NewString(), AssignmentID: id, Seq: 0, Kind: "photo", SubmittedBy: "t"})
	}); err != nil {
		t.Fatal(err)
	}
	if code := post("/stops/0/completed", "", ""); code != 200 {
		t.Fatalf("pickup: %d", code)
	}
	if code := post("/stops/1/proof", "application/json", `{"pin":"0427"}`); code != 201 {
		t.Fatalf("the right PIN: %d", code)
	}
	if code := post("/stops/1/completed", "", ""); code != 200 {
		t.Fatalf("drop: %d", code)
	}
}
