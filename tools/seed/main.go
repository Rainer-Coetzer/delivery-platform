// Command seed loads demo data through the gateway, as the bootstrap
// admin: a customer, three restaurants (each with a menu and a linked
// restaurant login), and three available drivers (each with a linked driver
// login). Safe to re-run: existing accounts are skipped, and restaurants
// and drivers are only created when there are none yet.
//
//	go run ./tools/seed
//
// Needs the stack started with docker-compose.dev.yml, which provides the
// bootstrap admin (admin@example.com). Every call goes through the gateway:
// backends have no host ports any more (audit P0-6).
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/rainercoetzer/delivery-platform/tools/internal/client"
)

var (
	gateway       = envOr("GATEWAY_URL", "http://localhost:8080")
	adminEmail    = envOr("SEED_ADMIN_EMAIL", "admin@example.com")
	adminPassword = envOr("SEED_ADMIN_PASSWORD", "admin-dev-password")
)

// Demo accounts share one password. They're dev data; never seed a real
// environment with this tool.
const demoPassword = "password123"

type restaurantSeed struct {
	Name, OpensAt, ClosesAt, Timezone string
	Lat, Lng                          float64
}

// All-Day Kitchen is what tools/e2e orders from. 00:00-00:00 reads as
// "closes before it opens", which domain.OpenAt treats as spanning midnight,
// so it is open around the clock, including whatever hour the e2e runs at.
var restaurants = []restaurantSeed{
	{"All-Day Kitchen", "00:00", "00:00", "Africa/Windhoek", -22.5650, 17.0750},
	{"Nando's Maerua", "09:00", "22:00", "Africa/Windhoek", -22.5608, 17.0658},
	{"Xwama Cultural Village", "08:00", "21:00", "Africa/Windhoek", -22.5750, 17.0850},
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
	var login struct{ Token string }
	if err := client.Do(http.MethodPost, gateway+"/auth/login", "", map[string]string{"email": adminEmail, "password": adminPassword}, &login); err != nil {
		return fmt.Errorf("log in as bootstrap admin %s (is the stack running with docker-compose.dev.yml?): %w", adminEmail, err)
	}
	admin := login.Token

	fmt.Println("-> customer")
	if err := register(map[string]string{"name": "Nangula Amutenya", "email": "nangula@example.com", "password": demoPassword}); err != nil {
		return err
	}

	fmt.Println("-> restaurants")
	var existing []map[string]any
	if err := client.Do(http.MethodGet, gateway+"/restaurants", admin, nil, &existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		fmt.Printf("   %d already exist, skipping\n", len(existing))
	} else {
		for _, r := range restaurants {
			var created struct{ ID string }
			body := map[string]any{"name": r.Name, "lat": r.Lat, "lng": r.Lng, "opensAt": r.OpensAt, "closesAt": r.ClosesAt, "timezone": r.Timezone}
			if err := client.Do(http.MethodPost, gateway+"/restaurants", admin, body, &created); err != nil {
				return err
			}
			for _, item := range menu {
				if err := client.Do(http.MethodPost, gateway+"/restaurants/"+created.ID+"/menu-items", admin, item, nil); err != nil {
					return err
				}
			}
			email := "owner+" + slug(r.Name) + "@example.com"
			if err := createUser(admin, r.Name+" staff", email, "restaurant", created.ID); err != nil {
				return err
			}
			fmt.Printf("   %s (%s), login %s\n", r.Name, created.ID, email)
		}
	}

	fmt.Println("-> drivers")
	var existingDrivers []map[string]any
	if err := client.Do(http.MethodGet, gateway+"/drivers", admin, nil, &existingDrivers); err != nil {
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
		if err := client.Do(http.MethodPost, gateway+"/drivers", admin, body, &created); err != nil {
			return err
		}
		if err := client.Do(http.MethodPatch, gateway+"/drivers/"+created.ID+"/status", admin, map[string]string{"status": "AVAILABLE"}, nil); err != nil {
			return err
		}
		email := "driver+" + slug(name) + "@example.com"
		if err := createUser(admin, name, email, "driver", created.ID); err != nil {
			return err
		}
		fmt.Printf("   %s (%s) AVAILABLE, login %s\n", name, created.ID, email)
	}
	return nil
}

func register(body map[string]string) error {
	err := client.Do(http.MethodPost, gateway+"/auth/register", "", body, nil)
	if isStatus(err, http.StatusConflict) {
		fmt.Printf("   %s already registered\n", body["email"])
		return nil
	}
	if err == nil {
		fmt.Printf("   registered %s\n", body["email"])
	}
	return err
}

// createUser is admin onboarding: a login linked (refId) to the restaurant
// or driver just created.
func createUser(admin, name, email, role, refID string) error {
	body := map[string]string{"name": name, "email": email, "password": demoPassword, "role": role, "refId": refID}
	err := client.Do(http.MethodPost, gateway+"/users", admin, body, nil)
	if isStatus(err, http.StatusConflict) {
		return nil
	}
	return err
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
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
