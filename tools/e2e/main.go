// Command e2e runs the core end-to-end suite against the running stack for
// the pack it runs (PACK, else config/brand.yaml's): it places the job described in
// packs/<PACK>/seed.yaml through the gateway, does what a person would
// (approve it, attach its paperwork) when the pack asks for that, and
// waits for the completed milestone, printing each state on the way. Then
// it checks what every pack must get right: each stop done, and proof
// captured wherever the pack requires it.
//
// It needs the stack running with DEMO_AUTOPILOT on (docker-compose.dev.yml)
// and seeded; scripts/e2e.sh does both before calling this.
//
// Exit codes: 0 completed, 1 anything else (cancelled, timeout, API error),
// always with the reason on stderr.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/tools/internal/client"
	"github.com/rainercoetzer/delivery-platform/tools/internal/seeddata"
)

var (
	gateway       = envOr("GATEWAY_URL", "http://localhost:8080")
	adminEmail    = envOr("SEED_ADMIN_EMAIL", "admin@example.com")
	adminPassword = envOr("SEED_ADMIN_PASSWORD", "admin-dev-password")
)

const demoPassword = "password123" // tools/seed's

type merchant struct {
	ID, Name string
}

type catalogueItem struct {
	ID         string
	Name       string
	PriceCents int64
}

type proofRef struct{ Kind, AssignmentID string }

type stop struct {
	Seq           int
	Kind          string
	Proof         string
	CompletedAt   *time.Time
	CapturedProof *proofRef
}

type job struct {
	ID           string
	Status       string // the pack's state name, for the log
	Milestone    string // what the run decides on
	CancelReason string
	TotalCents   int64
	Stops        []stop
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "E2E FAILED:", err)
		os.Exit(1)
	}
}

func run() error {
	timeout := 5 * time.Minute // freight's multi-drop route takes the dev autopilot about two
	if v := os.Getenv("E2E_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("E2E_TIMEOUT=%q is not a duration (e.g. 5m)", v)
		}
		timeout = d
	}
	pack, err := app.LoadPack()
	if err != nil {
		return err
	}
	seed, err := seeddata.Load(pack.Name)
	if err != nil {
		return err
	}
	fmt.Printf("-> pack %s\n", pack.Name)

	customer, err := login(seed.E2E.Customer, demoPassword)
	if err != nil {
		return fmt.Errorf("log in as the seeded customer %s (did seed run with the same PACK?): %w", seed.E2E.Customer, err)
	}

	body, merchantID, err := jobRequest(pack, seed.E2E, customer)
	if err != nil {
		return err
	}
	if err := securitySmoke(customer, merchantID); err != nil {
		return err
	}

	var o job
	if err := client.Do(http.MethodPost, gateway+"/jobs", customer, body, &o); err != nil {
		return err
	}
	fmt.Printf("-> placed job %s (%d stops, total %d cents)\n", o.ID, len(o.Stops), o.TotalCents)
	if o.TotalCents <= 0 {
		return fmt.Errorf("job total is %d: pricing is broken", o.TotalCents)
	}

	if err := attachDocument(pack, seed.E2E, customer, o.ID); err != nil {
		return err
	}
	if err := approve(pack, o.ID); err != nil {
		return err
	}
	if err := waitForCompletion(customer, &o, timeout); err != nil {
		return err
	}
	return checkStops(pack, customer, &o)
}

// jobRequest builds the job the pack's stop template takes, from the
// seed's e2e section.
func jobRequest(pack *packs.Pack, e seeddata.E2E, token string) (map[string]any, string, error) {
	body := map[string]any{"paymentMethod": e.PaymentMethod}
	switch pack.Job.Stops {
	case packs.StopsMerchantToCustomer:
		var ms []merchant
		if err := client.Do(http.MethodGet, gateway+"/merchants", token, nil, &ms); err != nil {
			return nil, "", err
		}
		i := slices.IndexFunc(ms, func(m merchant) bool { return m.Name == e.Merchant })
		if i < 0 {
			return nil, "", fmt.Errorf("merchant %q not found among %d; run tools/seed against a fresh database", e.Merchant, len(ms))
		}
		var items []catalogueItem
		if err := client.Do(http.MethodGet, gateway+"/merchants/"+ms[i].ID+"/catalogue-items", token, nil, &items); err != nil {
			return nil, "", err
		}
		if len(items) == 0 {
			return nil, "", fmt.Errorf("%s has no catalogue items", e.Merchant)
		}
		// Only ids and quantities: the server prices from the catalogue.
		body["merchantId"] = ms[i].ID
		body["items"] = []map[string]any{{"catalogueItemId": items[0].ID, "quantity": 1}}
		body["dropoff"] = e.Dropoff
		return body, ms[i].ID, nil
	case packs.StopsPointToPoint:
		body["pickup"], body["dropoff"], body["packages"] = e.Pickup, e.Dropoff, e.Packages
	case packs.StopsMultiStop:
		stops := make([]map[string]any, len(e.Stops))
		for i, s := range e.Stops {
			stops[i] = map[string]any{"kind": s.Kind, "location": map[string]any{"lat": s.Lat, "lng": s.Lng, "label": s.Label}}
		}
		body["stops"], body["packages"] = stops, e.Packages
	default:
		return nil, "", fmt.Errorf("no e2e for stop template %q", pack.Job.Stops)
	}
	return body, "", nil
}

// approve sets every fact the pack lets an admin vouch for (the approval
// module), as a dispatcher would.
func approve(pack *packs.Pack, jobID string) error {
	var facts []string
	for _, f := range pack.Lifecycle.Facts {
		if slices.Contains(f.By, "admin") {
			facts = append(facts, f.Name)
		}
	}
	if len(facts) == 0 {
		return nil
	}
	admin, err := login(adminEmail, adminPassword)
	if err != nil {
		return fmt.Errorf("log in as admin to approve: %w", err)
	}
	for _, f := range facts {
		if err := client.Do(http.MethodPost, gateway+"/jobs/"+jobID+"/facts/"+f, admin, nil, nil); err != nil {
			return fmt.Errorf("admin sets %s: %w", f, err)
		}
		fmt.Printf("-> admin set %s\n", f)
	}
	return nil
}

// attachDocument uploads the seed's document (the documents module) and
// checks it's listed.
func attachDocument(pack *packs.Pack, e seeddata.E2E, token, jobID string) error {
	if !pack.Has(packs.ModDocuments) || e.Document == nil {
		return nil
	}
	content := "%PDF-1.4 e2e " + e.Document.Name
	if err := client.Upload(gateway+"/jobs/"+jobID+"/documents?name="+e.Document.Name, token, e.Document.Type, strings.NewReader(content)); err != nil {
		return fmt.Errorf("upload %s: %w", e.Document.Name, err)
	}
	var docs []struct{ Name string }
	if err := client.Do(http.MethodGet, gateway+"/jobs/"+jobID+"/documents", token, nil, &docs); err != nil {
		return err
	}
	if len(docs) != 1 || docs[0].Name != e.Document.Name {
		return fmt.Errorf("documents after upload: %+v", docs)
	}
	fmt.Printf("-> attached %s\n", e.Document.Name)
	return nil
}

func waitForCompletion(token string, o *job, timeout time.Duration) error {
	start := time.Now()
	last := ""
	for time.Since(start) < timeout {
		if err := client.Do(http.MethodGet, gateway+"/jobs/"+o.ID, token, nil, o); err != nil {
			return err
		}
		if o.Status != last {
			fmt.Printf("   %5.1fs  %s\n", time.Since(start).Seconds(), o.Status)
			last = o.Status
		}
		switch o.Milestone {
		case "completed":
			fmt.Printf("-> %s in %.0fs\n", o.Status, time.Since(start).Seconds())
			return nil
		case "cancelled":
			return fmt.Errorf("job %s was cancelled: %s", o.ID, o.CancelReason)
		}
		time.Sleep(2 * time.Second)
	}
	hint := ""
	if o.Milestone != "created" {
		hint = " (stuck after confirmation usually means DEMO_AUTOPILOT is off: start with -f docker-compose.dev.yml)"
	}
	return fmt.Errorf("job %s still %s after %s%s", o.ID, last, timeout, hint)
}

// checkStops: every stop done, and proof captured wherever the pack asks
// for it. The autopilot records "simulated" proof; a real agent's app
// records the real kind. The customer can open each one.
func checkStops(pack *packs.Pack, token string, o *job) error {
	for _, s := range o.Stops {
		if s.CompletedAt == nil {
			return fmt.Errorf("the job completed but stop %d (%s) isn't done", s.Seq, s.Kind)
		}
		if s.Proof == "" {
			continue
		}
		if s.CapturedProof == nil {
			return fmt.Errorf("stop %d needs a %s but none was captured", s.Seq, s.Proof)
		}
		path := fmt.Sprintf("/assignments/%s/stops/%d/proof", s.CapturedProof.AssignmentID, s.Seq)
		if err := client.Do(http.MethodGet, gateway+path, token, nil, nil); err != nil {
			return fmt.Errorf("the customer opens the proof of stop %d: %w", s.Seq, err)
		}
		fmt.Printf("   ok  stop %d (%s): %s captured (%s)\n", s.Seq, s.Kind, s.Proof, s.CapturedProof.Kind)
	}
	fmt.Printf("-> E2E PASSED: pack %s, %d stops\n", pack.Name, len(o.Stops))
	return nil
}

func login(email, password string) (string, error) {
	var out struct{ Token string }
	err := client.Do(http.MethodPost, gateway+"/auth/login", "", map[string]string{"email": email, "password": password}, &out)
	return out.Token, err
}

// securitySmoke checks the audit P0 holes stay closed against the running
// stack. Each expectation names the finding it guards; the catalogue one
// runs only on packs that have merchants.
func securitySmoke(customerToken, merchantID string) error {
	type check struct {
		name         string
		method, path string
		token        string
		body         any
		want         int
	}
	checks := []check{
		{"P0-3 public signup as admin", http.MethodPost, "/auth/register", "",
			map[string]string{"name": "x", "email": "e2e-evil@example.com", "password": "password123", "role": "admin"}, 400},
		{"P0-5 customer reads admin reports", http.MethodGet, "/admin/overview", customerToken, nil, 403},
		{"P0-5 customer lists agents", http.MethodGet, "/agents", customerToken, nil, 403},
		{"P0-5 customer reads the dispatch queue", http.MethodGet, "/assignments/queue", customerToken, nil, 403},
		{"P0-5 no token", http.MethodGet, "/jobs?customerId=anyone", "", nil, 401},
		{"P0-7 websocket without token", http.MethodGet, "/ws?jobId=x", "", nil, 401},
	}
	if merchantID != "" {
		checks = append(checks, check{"P0-5 customer edits a catalogue", http.MethodPost, "/merchants/" + merchantID + "/catalogue-items", customerToken,
			map[string]any{"name": "x", "priceCents": 1, "stockQty": 1, "available": true}, 403})
	}
	fmt.Println("-> security smoke checks")
	for _, c := range checks {
		err := client.Do(c.method, gateway+c.path, c.token, c.body, nil)
		got := 200
		var se *client.StatusError
		if errors.As(err, &se) {
			got = se.Status
		} else if err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		if got != c.want {
			return fmt.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
		fmt.Printf("   ok  %s -> %d\n", c.name, got)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
