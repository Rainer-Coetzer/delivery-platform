//go:build integration

package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/blob"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/store"
)

func TestDocuments(t *testing.T) {
	pack := strings.NewReplacer(
		"name: parcel", "name: docs",
		"modules: [packages, capacity]", "modules: [packages, capacity, documents]",
	).Replace(parcelPack) + "  documents: { max_bytes: 32, max_per_job: 2, allowed_types: [application/pdf, image/png] }\n"
	p, err := packs.Load(fstest.MapFS{
		"docs/pack.yaml":      {Data: []byte(pack)},
		"docs/lifecycle.yaml": {Data: []byte(parcelLifecycle)},
	}, "docs", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	id := storedJob(t, pool, "BOOKED", lifecycle.Created) // customer c1, merchant m1

	do := func(c *auth.Claims, method, path, ct, body string) (int, string, string) {
		t.Helper()
		a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: as(c), Pack: p, Blobs: blobs}
		r := chi.NewRouter()
		a.Routes(r)
		req := httptest.NewRequest(method, "/jobs/"+id+path, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String(), rec.Header().Get("Content-Disposition")
	}
	customer := &auth.Claims{UserID: "c1", Role: auth.RoleCustomer}
	merchant := &auth.Claims{UserID: "o1", Role: auth.RoleMerchant, RefID: "m1"}
	stranger := &auth.Claims{UserID: "c2", Role: auth.RoleCustomer}

	if c, b, _ := do(customer, "POST", "/documents?name=customs.pdf", "application/pdf", "%PDF-1.7 form"); c != 201 {
		t.Fatalf("customer uploads: %d %s", c, b)
	}
	if c, b, _ := do(customer, "POST", "/documents?name=x.exe", "application/octet-stream", "MZ"); c != 415 {
		t.Fatalf("a type the pack doesn't allow: %d %s", c, b)
	}
	if c, b, _ := do(customer, "POST", "/documents?name=big.pdf", "application/pdf", strings.Repeat("x", 33)); c != 413 {
		t.Fatalf("over max_bytes: %d %s", c, b)
	}
	if c, b, _ := do(customer, "POST", "/documents", "application/pdf", "x"); c != 400 {
		t.Fatalf("no name: %d %s", c, b)
	}
	if c, b, _ := do(stranger, "POST", "/documents?name=a.pdf", "application/pdf", "x"); c != 403 {
		t.Fatalf("someone else's job: %d %s", c, b)
	}
	if c, b, _ := do(merchant, "POST", "/documents?name=invoice.png", "image/png", "png"); c != 201 {
		t.Fatalf("the merchant uploads too: %d %s", c, b)
	}
	if c, b, _ := do(customer, "POST", "/documents?name=third.pdf", "application/pdf", "x"); c != 409 {
		t.Fatalf("max_per_job: %d %s", c, b)
	}

	c, body, _ := do(merchant, "GET", "/documents", "", "")
	var docs []Document
	if err := json.Unmarshal([]byte(body), &docs); c != 200 || err != nil || len(docs) != 2 || docs[0].Name != "customs.pdf" || docs[1].UploadedRole != "merchant" {
		t.Fatalf("list: %d %s", c, body)
	}
	c, body, disposition := do(merchant, "GET", "/documents/"+docs[0].ID, "", "")
	if c != 200 || body != "%PDF-1.7 form" || disposition != `attachment; filename="customs.pdf"` {
		t.Fatalf("download: %d %q %q", c, body, disposition)
	}
	if c, _, _ := do(stranger, "GET", "/documents/"+docs[0].ID, "", ""); c != 403 {
		t.Fatalf("a stranger can't download: %d", c)
	}
	if c, _, _ := do(customer, "GET", "/documents/nope", "", ""); c != 404 {
		t.Fatalf("unknown document: %d", c)
	}
}

// max_per_job holds when uploads race: the count and the insert happen
// under the job's lock.
func TestDocumentLimitHoldsUnderParallelUploads(t *testing.T) {
	pack := strings.NewReplacer(
		"name: parcel", "name: docs",
		"modules: [packages, capacity]", "modules: [packages, capacity, documents]",
	).Replace(parcelPack) + "  documents: { max_bytes: 32, max_per_job: 2, allowed_types: [application/pdf] }\n"
	p, err := packs.Load(fstest.MapFS{"docs/pack.yaml": {Data: []byte(pack)}, "docs/lifecycle.yaml": {Data: []byte(parcelLifecycle)}},
		"docs", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t, migrations.FS)
	id := storedJob(t, pool, "BOOKED", lifecycle.Created)
	a := &API{Pool: pool, Store: store.New(pool), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth: as(&auth.Claims{UserID: "c1", Role: auth.RoleCustomer}), Pack: p, Blobs: blobs}
	r := chi.NewRouter()
	a.Routes(r)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/jobs/"+id+"/documents?name=a.pdf", strings.NewReader("%PDF"))
			req.Header.Set("Content-Type", "application/pdf")
			r.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM job_documents WHERE job_id = $1`, id).Scan(&n); err != nil || n != 2 {
		t.Fatalf("8 parallel uploads left %d documents; the limit is 2 (%v)", n, err)
	}
}
