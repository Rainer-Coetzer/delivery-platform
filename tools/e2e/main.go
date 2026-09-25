// Command e2e places one job through the gateway and waits for it to
// reach DELIVERED, printing every status change on the way. It needs the
// stack running with DEMO_AUTOPILOT on (docker-compose.dev.yml) and seeded
// data; scripts/e2e.sh does both before calling this.
//
// Exit codes: 0 delivered, 1 anything else (cancelled, timeout, API error),
// always with the reason on stderr.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rainercoetzer/delivery-platform/tools/internal/client"
)

var gateway = envOr("GATEWAY_URL", "http://localhost:8080")

const merchantName = "All-Day Kitchen" // seeded by tools/seed, open 24h

type merchant struct {
	ID, Name string
	Lat, Lng float64
}

type catalogueItem struct {
	ID         string
	Name       string
	PriceCents int64
}

type job struct {
	ID           string
	Status       string // the pack's state name, for the log
	Milestone    string // what the run decides on
	CancelReason string
	TotalCents   int64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "E2E FAILED:", err)
		os.Exit(1)
	}
}

func run() error {
	timeout := 3 * time.Minute
	if v := os.Getenv("E2E_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("E2E_TIMEOUT=%q is not a duration (e.g. 3m)", v)
		}
		timeout = d
	}

	var login struct {
		Token string
		User  struct{ ID, Name string }
	}
	creds := map[string]string{"email": "nangula@example.com", "password": "password123"}
	if err := client.Do(http.MethodPost, gateway+"/auth/login", "", creds, &login); err != nil {
		return fmt.Errorf("log in as the seeded customer (did seed run?): %w", err)
	}
	fmt.Printf("-> logged in as %s (%s)\n", login.User.Name, login.User.ID)

	var rs []merchant
	if err := client.Do(http.MethodGet, gateway+"/merchants", login.Token, nil, &rs); err != nil {
		return err
	}
	var r *merchant
	for i := range rs {
		if rs[i].Name == merchantName {
			r = &rs[i]
		}
	}
	if r == nil {
		return fmt.Errorf("merchant %q not found among %d merchants; run go run ./tools/seed against a fresh database", merchantName, len(rs))
	}

	var items []catalogueItem
	if err := client.Do(http.MethodGet, gateway+"/merchants/"+r.ID+"/catalogue-items", login.Token, nil, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%s has no catalogue items", r.Name)
	}
	item := items[0]

	if err := securitySmoke(login.Token, r.ID); err != nil {
		return err
	}

	// Only item ids and quantities: the server prices from the catalogue.
	// Cash always authorizes, so a random simulated card decline can't make
	// the e2e flaky. The failure path gets its own tests.
	body := map[string]any{
		"merchantId":    r.ID,
		"items":         []map[string]any{{"catalogueItemId": item.ID, "quantity": 1}},
		"paymentMethod": "cash",
		"dropoff":       map[string]float64{"lat": -22.5594, "lng": 17.0832},
	}
	var o job
	if err := client.Do(http.MethodPost, gateway+"/jobs", login.Token, body, &o); err != nil {
		return err
	}
	fmt.Printf("-> placed job %s (%s x1, total %d cents)\n", o.ID, item.Name, o.TotalCents)
	if o.TotalCents < item.PriceCents {
		return fmt.Errorf("job total %d is below the item price %d: server-side pricing is broken", o.TotalCents, item.PriceCents)
	}

	start := time.Now()
	last := ""
	for time.Since(start) < timeout {
		if err := client.Do(http.MethodGet, gateway+"/jobs/"+o.ID, login.Token, nil, &o); err != nil {
			return err
		}
		if o.Status != last {
			fmt.Printf("   %5.1fs  %s\n", time.Since(start).Seconds(), o.Status)
			last = o.Status
		}
		switch o.Milestone {
		case "completed":
			fmt.Printf("-> E2E PASSED: %s in %.0fs\n", o.Status, time.Since(start).Seconds())
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

// securitySmoke checks the audit P0 holes stay closed against the running
// stack. Each expectation names the finding it guards.
func securitySmoke(customerToken, merchantID string) error {
	checks := []struct {
		name         string
		method, path string
		token        string
		body         any
		want         int
	}{
		{"P0-3 public signup as admin", http.MethodPost, "/auth/register", "",
			map[string]string{"name": "x", "email": "e2e-evil@example.com", "password": "password123", "role": "admin"}, 400},
		{"P0-5 customer reads admin reports", http.MethodGet, "/admin/overview", customerToken, nil, 403},
		{"P0-5 customer edits a catalogue", http.MethodPost, "/merchants/" + merchantID + "/catalogue-items", customerToken,
			map[string]any{"name": "x", "priceCents": 1, "stockQty": 1, "available": true}, 403},
		{"P0-5 customer lists agents", http.MethodGet, "/agents", customerToken, nil, 403},
		{"P0-5 no token", http.MethodGet, "/jobs?customerId=anyone", "", nil, 401},
		{"P0-7 websocket without token", http.MethodGet, "/ws?jobId=x", "", nil, 401},
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
