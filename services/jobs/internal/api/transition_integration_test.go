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
	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

func storedJob(t *testing.T, pool *db.Pool, status, milestone string) string {
	t.Helper()
	j := &domain.Job{
		ID: uuid.NewString(), TenantID: "default", Pack: "food", CustomerID: "c1", CustomerName: "C", MerchantID: "m1", MerchantName: "Kapana",
		Status: status, Milestone: milestone, Facts: map[string]bool{}, PaymentMethod: "card",
		Stops: []domain.Stop{
			{Stop: events.Stop{Seq: 0, Kind: events.StopPickup, MerchantID: "m1"}},
			{Stop: events.Stop{Seq: 1, Kind: events.StopDropoff}},
		},
	}
	err := pool.WithTx(context.Background(), func(tx pgx.Tx) error { return store.Create(context.Background(), tx, j, status, nil) })
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

// Who may move a job by hand is the pack's lifecycle (food: the customer
// until the kitchen starts, an admin any time), and only on jobs they can
// see.
func TestTransitionEndpoint(t *testing.T) {
	pool := dbtest.New(t, migrations.FS)
	customer := &auth.Claims{UserID: "c1", Role: auth.RoleCustomer}
	stranger := &auth.Claims{UserID: "c2", Role: auth.RoleCustomer}
	merchant := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "m1"}
	admin := &auth.Claims{UserID: "x", Role: auth.RoleAdmin}

	created := storedJob(t, pool, "CREATED", lifecycle.Created)
	preparing := storedJob(t, pool, "PREPARING", lifecycle.Confirmed)
	cases := []struct {
		name   string
		claims *auth.Claims
		job    string
		want   int
	}{
		{"someone else's job", stranger, created, 403},
		{"merchant has no cancel in food", merchant, created, 409},
		{"customer too late: the kitchen started", customer, preparing, 409},
		{"customer cancels before the kitchen starts", customer, created, 200},
		{"admin cancels any time", admin, preparing, 200},
		{"nothing moves a cancelled job", admin, created, 409},
		{"unknown job", admin, uuid.NewString(), 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c.claims), Pack: foodPack(t)}
			r := chi.NewRouter()
			a.Routes(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("POST", "/jobs/"+c.job+"/transitions", strings.NewReader(`{"to":"CANCELLED","reason":"changed my mind"}`)))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}

	var cancelledEvents int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE event_type = $1`, events.JobCancelled).Scan(&cancelledEvents); err != nil {
		t.Fatal(err)
	}
	if cancelledEvents != 2 {
		t.Fatalf("two cancellations should publish two jobs.cancelled (refund, stock), got %d", cancelledEvents)
	}
	j, err := store.New(pool).Get(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if j.CancelReason != "changed my mind" {
		t.Fatalf("cancel reason = %q", j.CancelReason)
	}
}
