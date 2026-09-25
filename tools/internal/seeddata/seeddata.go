// Package seeddata reads a pack's demo data, packs/<name>/seed.yaml: the
// accounts, merchants and agents tools/seed creates, and the job tools/e2e
// places. It's read from disk, never embedded, so demo data doesn't ship in
// the binary a buyer deploys.
package seeddata

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

type Seed struct {
	Customers []Customer `yaml:"customers"`
	Merchants []Merchant `yaml:"merchants"`
	Agents    []Agent    `yaml:"agents"`
	E2E       E2E        `yaml:"e2e"`
}

type Customer struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type Merchant struct {
	Name      string          `yaml:"name"`
	OpensAt   string          `yaml:"opens_at"`
	ClosesAt  string          `yaml:"closes_at"`
	Timezone  string          `yaml:"timezone"`
	Lat       float64         `yaml:"lat"`
	Lng       float64         `yaml:"lng"`
	Catalogue []CatalogueItem `yaml:"catalogue"`
}

type CatalogueItem struct {
	Name       string `yaml:"name"`
	PriceCents int64  `yaml:"price_cents"`
	StockQty   int    `yaml:"stock_qty"`
}

type Agent struct {
	Name         string   `yaml:"name"`
	Vehicle      string   `yaml:"vehicle"`
	Lat          float64  `yaml:"lat"`
	Lng          float64  `yaml:"lng"`
	CapacityG    int64    `yaml:"capacity_g"`
	CapacityCm3  int64    `yaml:"capacity_cm3"`
	Capabilities []string `yaml:"capabilities"`
}

// E2E is the job the end-to-end run places. Which fields count depends on
// the pack's stop template: merchant + dropoff (merchant_to_customer),
// pickup + dropoff + packages (point_to_point), stops + packages
// (multi_stop).
type E2E struct {
	Customer      string    `yaml:"customer"`
	PaymentMethod string    `yaml:"payment_method"`
	Merchant      string    `yaml:"merchant"`
	Pickup        *Point    `yaml:"pickup"`
	Dropoff       *Point    `yaml:"dropoff"`
	Stops         []Stop    `yaml:"stops"`
	Packages      []Package `yaml:"packages"`
	Document      *Document `yaml:"document"`
}

type Point struct {
	Lat   float64 `yaml:"lat" json:"lat"`
	Lng   float64 `yaml:"lng" json:"lng"`
	Label string  `yaml:"label" json:"label,omitempty"`
}

type Stop struct {
	Kind  string  `yaml:"kind"`
	Lat   float64 `yaml:"lat"`
	Lng   float64 `yaml:"lng"`
	Label string  `yaml:"label"`
}

type Package struct {
	Description        string `yaml:"description" json:"description"`
	Quantity           int    `yaml:"quantity" json:"quantity"`
	WeightG            int64  `yaml:"weight_g" json:"weightGrams"`
	LengthMm           int    `yaml:"length_mm" json:"lengthMm"`
	WidthMm            int    `yaml:"width_mm" json:"widthMm"`
	HeightMm           int    `yaml:"height_mm" json:"heightMm"`
	DeclaredValueCents int64  `yaml:"declared_value_cents" json:"declaredValueCents,omitempty"`
}

type Document struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

// Load reads packs/<pack>/seed.yaml (or under PACKS_DIR), strictly: a
// typo in the demo data should fail the seed, not seed half of it.
func Load(pack string) (*Seed, error) {
	dir := os.Getenv("PACKS_DIR")
	if dir == "" {
		dir = "packs"
	}
	p := filepath.Join(dir, pack, "seed.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("pack %s has no demo data (%w); run from the repo root, or set PACKS_DIR", pack, err)
	}
	var s Seed
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if s.E2E.Customer == "" || s.E2E.PaymentMethod == "" {
		return nil, fmt.Errorf("%s: e2e needs a customer and a payment_method", p)
	}
	return &s, nil
}
