// Command seed loads demo data: a customer and an admin account, three
// restaurants with menus, and three available drivers. Safe to re-run: it
// skips accounts that already exist and only creates restaurants and
// drivers when there are none yet.
//
//	go run ./tools/seed
//
// Restaurants and drivers are created directly on their services' host
// ports, because there is no admin onboarding flow yet. Audit P0-6
// (Phase 2) moves this behind the gateway as an authenticated admin.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/rainercoetzer/delivery-platform/tools/internal/client"
)

var (
	gateway    = envOr("GATEWAY_URL", "http://localhost:8080")
	restaurant = envOr("RESTAURANT_URL", "http://localhost:8082")
	delivery   = envOr("DELIVERY_URL", "http://localhost:8085")
)

type restaurantSeed struct {
	Name, OpensAt, ClosesAt string
	Lat, Lng                float64
}

// All-Day Kitchen is what tools/e2e orders from. 00:00-00:00 reads as
// "closes before it opens", which domain.OpenAt treats as spanning midnight,
// so it is open around the clock, including whatever hour the e2e runs at.
var restaurants = []restaurantSeed{
	{"All-Day Kitchen", "00:00", "00:00", -22.5650, 17.0750},
	{"Nando's Maerua", "09:00", "22:00", -22.5608, 17.0658},
	{"Xwama Cultural Village", "08:00", "21:00", -22.5750, 17.0850},
}

var menu = []map[string]any{
	{"name": "Quarter Chicken & Chips", "priceCents": 8500, "stockQty": 500, "available": true},
	{"name": "Kapana Platter", "priceCents": 6500, "stockQty": 500, "available": true},
}

var drivers = []string{"Johannes Shikongo", "Maria Nakale", "Petrus Amupolo"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed failed:", err)
		os.Exit(1)
	}
	fmt.Println("-> seed complete")
}

func run() error {
	fmt.Println("-> accounts")
	// Registering an admin through the public endpoint only works while
	// P0-3 is open; Phase 2 replaces this with an env-seeded bootstrap admin.
	for _, acct := range []map[string]string{
		{"name": "Nangula Amutenya", "email": "nangula@example.com", "password": "password123", "role": "customer"},
		{"name": "Admin", "email": "admin@example.com", "password": "password123", "role": "admin"},
	} {
		err := client.Do(http.MethodPost, gateway+"/auth/register", "", acct, nil)
		if isStatus(err, http.StatusConflict) {
			fmt.Printf("   %s already registered\n", acct["email"])
			continue
		}
		if err != nil {
			return err
		}
		fmt.Printf("   registered %s\n", acct["email"])
	}

	fmt.Println("-> restaurants")
	var existing []map[string]any
	if err := client.Do(http.MethodGet, restaurant+"/restaurants", "", nil, &existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		fmt.Printf("   %d already exist, skipping\n", len(existing))
	} else {
		for _, r := range restaurants {
			var created struct{ ID string }
			body := map[string]any{"name": r.Name, "lat": r.Lat, "lng": r.Lng, "opensAt": r.OpensAt, "closesAt": r.ClosesAt}
			if err := client.Do(http.MethodPost, restaurant+"/restaurants", "", body, &created); err != nil {
				return err
			}
			for _, item := range menu {
				if err := client.Do(http.MethodPost, restaurant+"/restaurants/"+created.ID+"/menu-items", "", item, nil); err != nil {
					return err
				}
			}
			fmt.Printf("   %s (%s)\n", r.Name, created.ID)
		}
	}

	fmt.Println("-> drivers")
	var existingDrivers []map[string]any
	if err := client.Do(http.MethodGet, delivery+"/drivers", "", nil, &existingDrivers); err != nil {
		return err
	}
	if len(existingDrivers) > 0 {
		fmt.Printf("   %d already exist, skipping\n", len(existingDrivers))
		return nil
	}
	for _, name := range drivers {
		var created struct{ ID string }
		// All start in the Windhoek CBD, a few km from every restaurant.
		body := map[string]any{"name": name, "vehicle": "bike", "lat": -22.5700, "lng": 17.0836}
		if err := client.Do(http.MethodPost, delivery+"/drivers", "", body, &created); err != nil {
			return err
		}
		if err := client.Do(http.MethodPatch, delivery+"/drivers/"+created.ID+"/status", "", map[string]string{"status": "AVAILABLE"}, nil); err != nil {
			return err
		}
		fmt.Printf("   %s (%s) AVAILABLE\n", name, created.ID)
	}
	return nil
}

func isStatus(err error, status int) bool {
	var se *client.StatusError
	return errors.As(err, &se) && se.Status == status
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
