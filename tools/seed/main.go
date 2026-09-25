// Command seed loads a pack's demo data through the gateway, as the
// bootstrap admin: customers, merchants with catalogues and linked logins
// (when the pack has the catalogue module), and available agents with
// their vehicles and linked logins. The data is packs/<PACK>/seed.yaml;
// PACK defaults to food, as on the server. Safe to re-run: existing
// accounts are skipped, and merchants and agents are only created when
// there are none yet.
//
//	go run ./tools/seed
//	PACK=courier go run ./tools/seed
//
// Needs the stack started with docker-compose.dev.yml, which provides the
// bootstrap admin (admin@example.com). Every call goes through the gateway:
// backends have no host ports (audit P0-6).
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

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

// Demo accounts share one password. They're dev data; never seed a real
// environment with this tool.
const demoPassword = "password123"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed failed:", err)
		os.Exit(1)
	}
	fmt.Println("-> seed complete")
}

func run() error {
	pack, err := app.LoadPack()
	if err != nil {
		return err
	}
	seed, err := seeddata.Load(pack.Name)
	if err != nil {
		return err
	}
	fmt.Printf("-> pack %s\n", pack.Name)

	var login struct{ Token string }
	if err := client.Do(http.MethodPost, gateway+"/auth/login", "", map[string]string{"email": adminEmail, "password": adminPassword}, &login); err != nil {
		return fmt.Errorf("log in as bootstrap admin %s (is the stack running with docker-compose.dev.yml?): %w", adminEmail, err)
	}
	admin := login.Token

	fmt.Println("-> customers")
	for _, c := range seed.Customers {
		if err := register(c.Name, c.Email); err != nil {
			return err
		}
	}

	if pack.Has(packs.ModCatalogue) {
		if err := seedMerchants(admin, seed.Merchants); err != nil {
			return err
		}
	} else if len(seed.Merchants) > 0 {
		return fmt.Errorf("seed.yaml lists merchants but pack %s has no catalogue module", pack.Name)
	}
	return seedAgents(admin, seed.Agents)
}

func seedMerchants(admin string, merchants []seeddata.Merchant) error {
	fmt.Println("-> merchants")
	var existing []map[string]any
	if err := client.Do(http.MethodGet, gateway+"/merchants", admin, nil, &existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		fmt.Printf("   %d already exist, skipping\n", len(existing))
		return nil
	}
	for _, m := range merchants {
		var created struct{ ID string }
		body := map[string]any{"name": m.Name, "lat": m.Lat, "lng": m.Lng, "opensAt": m.OpensAt, "closesAt": m.ClosesAt, "timezone": m.Timezone}
		if err := client.Do(http.MethodPost, gateway+"/merchants", admin, body, &created); err != nil {
			return err
		}
		for _, item := range m.Catalogue {
			body := map[string]any{"name": item.Name, "priceCents": item.PriceCents, "stockQty": item.StockQty, "available": true}
			if err := client.Do(http.MethodPost, gateway+"/merchants/"+created.ID+"/catalogue-items", admin, body, nil); err != nil {
				return err
			}
		}
		email := "owner+" + slug(m.Name) + "@example.com"
		if err := createUser(admin, m.Name+" staff", email, "merchant", created.ID); err != nil {
			return err
		}
		fmt.Printf("   %s (%s), login %s\n", m.Name, created.ID, email)
	}
	return nil
}

func seedAgents(admin string, agents []seeddata.Agent) error {
	fmt.Println("-> agents")
	var existing []map[string]any
	if err := client.Do(http.MethodGet, gateway+"/agents", admin, nil, &existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		fmt.Printf("   %d already exist, skipping\n", len(existing))
		return nil
	}
	for _, a := range agents {
		var created struct{ ID string }
		body := map[string]any{"name": a.Name, "vehicle": a.Vehicle, "lat": a.Lat, "lng": a.Lng,
			"capacityGrams": a.CapacityG, "capacityCm3": a.CapacityCm3, "capabilities": a.Capabilities}
		if err := client.Do(http.MethodPost, gateway+"/agents", admin, body, &created); err != nil {
			return err
		}
		if err := client.Do(http.MethodPatch, gateway+"/agents/"+created.ID+"/status", admin, map[string]string{"status": "AVAILABLE"}, nil); err != nil {
			return err
		}
		email := "agent+" + slug(a.Name) + "@example.com"
		if err := createUser(admin, a.Name, email, "agent", created.ID); err != nil {
			return err
		}
		fmt.Printf("   %s, %s (%s) AVAILABLE, login %s\n", a.Name, a.Vehicle, created.ID, email)
	}
	return nil
}

func register(name, email string) error {
	err := client.Do(http.MethodPost, gateway+"/auth/register", "", map[string]string{"name": name, "email": email, "password": demoPassword}, nil)
	if isStatus(err, http.StatusConflict) {
		fmt.Printf("   %s already registered\n", email)
		return nil
	}
	if err == nil {
		fmt.Printf("   registered %s\n", email)
	}
	return err
}

// createUser is admin onboarding: a login linked (refId) to the merchant
// or agent just created.
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
