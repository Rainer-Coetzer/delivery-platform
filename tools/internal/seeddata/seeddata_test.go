package seeddata

import (
	"os"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// Every shipped pack must be demo-able: its seed loads, and its e2e job has
// what the pack's stop template needs.
func TestShippedPacksHaveUsableDemoData(t *testing.T) {
	t.Setenv("PACKS_DIR", "../../../packs")
	known := lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}}
	for _, name := range packs.List(packfs.FS) {
		t.Run(name, func(t *testing.T) {
			p, err := packs.Load(packfs.FS, name, known)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Load(name)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Agents) == 0 {
				t.Fatal("no agents: nothing would ever be dispatched")
			}
			if !has(s.Customers, s.E2E.Customer) {
				t.Fatalf("the e2e customer %s isn't seeded", s.E2E.Customer)
			}
			e := s.E2E
			switch p.Job.Stops {
			case packs.StopsMerchantToCustomer:
				if e.Merchant == "" || e.Dropoff == nil || !hasMerchant(s.Merchants, e.Merchant) {
					t.Fatal("e2e needs a seeded merchant and a dropoff")
				}
			case packs.StopsPointToPoint:
				if e.Pickup == nil || e.Dropoff == nil || len(e.Packages) == 0 {
					t.Fatal("e2e needs pickup, dropoff and packages")
				}
			case packs.StopsMultiStop:
				if len(e.Stops) < 2 || len(e.Packages) == 0 {
					t.Fatal("e2e needs stops and packages")
				}
			}
			if p.Has(packs.ModCapacity) {
				for _, a := range s.Agents {
					if a.CapacityG <= 0 || a.CapacityCm3 <= 0 {
						t.Fatalf("with capacity on, agent %s without a capacity gets no jobs", a.Name)
					}
				}
			}
			if p.Has(packs.ModDocuments) && e.Document == nil {
				t.Fatal("the pack keeps documents; the e2e should attach one")
			}
		})
	}
}

func TestLoadIsStrict(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PACKS_DIR", dir)
	if err := os.MkdirAll(dir+"/x", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/x/seed.yaml", []byte("agents: [{name: A, vehicel: bike}]\ne2e: {customer: c, payment_method: cash}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("x"); err == nil {
		t.Fatal("a typo in demo data must fail")
	}
	if _, err := Load("missing"); err == nil {
		t.Fatal("a pack without seed.yaml must say so")
	}
}

func has(cs []Customer, email string) bool {
	for _, c := range cs {
		if c.Email == email {
			return true
		}
	}
	return false
}

func hasMerchant(ms []Merchant, name string) bool {
	for _, m := range ms {
		if m.Name == name {
			return true
		}
	}
	return false
}
