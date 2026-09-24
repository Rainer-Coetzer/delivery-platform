// Command e2e places one order through the gateway and waits for it to
// reach DELIVERED, printing every status change on the way. It needs the
// stack running with DEMO_AUTOPILOT on (docker-compose.dev.yml) and seeded
// data; scripts/e2e.sh does both before calling this.
//
// Exit codes: 0 delivered, 1 anything else (cancelled, timeout, API error),
// always with the reason on stderr.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rainercoetzer/delivery-platform/tools/internal/client"
)

var gateway = envOr("GATEWAY_URL", "http://localhost:8080")

const restaurantName = "All-Day Kitchen" // seeded by tools/seed, open 24h

type restaurant struct {
	ID, Name string
	Lat, Lng float64
}

type menuItem struct {
	ID         string
	Name       string
	PriceCents int64
}

type order struct {
	ID           string
	Status       string
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

	var rs []restaurant
	if err := client.Do(http.MethodGet, gateway+"/restaurants", login.Token, nil, &rs); err != nil {
		return err
	}
	var r *restaurant
	for i := range rs {
		if rs[i].Name == restaurantName {
			r = &rs[i]
		}
	}
	if r == nil {
		return fmt.Errorf("restaurant %q not found among %d restaurants; run go run ./tools/seed against a fresh database", restaurantName, len(rs))
	}

	var items []menuItem
	if err := client.Do(http.MethodGet, gateway+"/restaurants/"+r.ID+"/menu-items", login.Token, nil, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%s has no menu items", r.Name)
	}
	item := items[0]

	// Cash always authorizes, so a random simulated card decline can't make
	// the e2e flaky. The failure path gets its own tests.
	body := map[string]any{
		"customerId": login.User.ID, "customerName": login.User.Name,
		"restaurantId": r.ID, "restaurantName": r.Name,
		"items": []map[string]any{{
			"menuItemId": item.ID, "name": item.Name, "quantity": 1, "unitPriceCents": item.PriceCents,
		}},
		"paymentMethod": "cash",
		"pickup":        map[string]float64{"lat": r.Lat, "lng": r.Lng},
		"dropoff":       map[string]float64{"lat": -22.5594, "lng": 17.0832},
	}
	var o order
	if err := client.Do(http.MethodPost, gateway+"/orders", login.Token, body, &o); err != nil {
		return err
	}
	fmt.Printf("-> placed order %s (%s x1, total %d cents)\n", o.ID, item.Name, o.TotalCents)

	start := time.Now()
	last := ""
	for time.Since(start) < timeout {
		if err := client.Do(http.MethodGet, gateway+"/orders/"+o.ID, login.Token, nil, &o); err != nil {
			return err
		}
		if o.Status != last {
			fmt.Printf("   %5.1fs  %s\n", time.Since(start).Seconds(), o.Status)
			last = o.Status
		}
		switch o.Status {
		case "DELIVERED":
			fmt.Printf("-> E2E PASSED: delivered in %.0fs\n", time.Since(start).Seconds())
			return nil
		case "CANCELLED":
			return fmt.Errorf("order %s was cancelled: %s", o.ID, o.CancelReason)
		}
		time.Sleep(2 * time.Second)
	}
	hint := ""
	if last == "PREPARING" || last == "READY" || last == "OUT_FOR_DELIVERY" {
		hint = " (stuck here usually means DEMO_AUTOPILOT is off: start with -f docker-compose.dev.yml)"
	}
	return fmt.Errorf("order %s still %s after %s%s", o.ID, last, timeout, hint)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
