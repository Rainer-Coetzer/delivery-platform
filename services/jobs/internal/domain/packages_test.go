package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

var limits = &packs.PackagesSettings{MaxLines: 3, MaxQuantity: 5, MaxWeightGrams: 30000, MaxDimensionMm: 1500, MaxDeclaredValueCents: 1000000}

func box() RequestedPackage {
	return RequestedPackage{Description: "Shoebox", Quantity: 2, WeightGrams: 1500, LengthMm: 330, WidthMm: 190, HeightMm: 120, DeclaredValueCents: 50000}
}

func TestPackageLines(t *testing.T) {
	mutate := func(f func(*RequestedPackage)) []RequestedPackage {
		p := box()
		f(&p)
		return []RequestedPackage{p}
	}
	cases := []struct {
		name string
		req  []RequestedPackage
		want string // "" = valid
	}{
		{"valid", []RequestedPackage{box()}, ""},
		{"none", nil, "at least one package"},
		{"too many lines", []RequestedPackage{box(), box(), box(), box()}, "at most 3 package lines"},
		{"blank description", mutate(func(p *RequestedPackage) { p.Description = "  " }), "needs a description"},
		{"long description", mutate(func(p *RequestedPackage) { p.Description = strings.Repeat("x", 201) }), "longer than 200"},
		{"zero quantity", mutate(func(p *RequestedPackage) { p.Quantity = 0 }), "quantity must be 1-5"},
		{"too heavy", mutate(func(p *RequestedPackage) { p.WeightGrams = 30001 }), "weight must be 1-30000 g"},
		{"weightless", mutate(func(p *RequestedPackage) { p.WeightGrams = 0 }), "weight must be"},
		{"too long", mutate(func(p *RequestedPackage) { p.LengthMm = 1501 }), "length must be 1-1500 mm"},
		{"flat", mutate(func(p *RequestedPackage) { p.HeightMm = 0 }), "height must be"},
		{"negative value", mutate(func(p *RequestedPackage) { p.DeclaredValueCents = -1 }), "declared value"},
		{"over-insured", mutate(func(p *RequestedPackage) { p.DeclaredValueCents = 1000001 }), "declared value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, err := PackageLines(c.req, limits)
			if c.want == "" {
				if err != nil || len(items) != 1 || items[0].Kind != LinePackage || items[0].Name != "Shoebox" {
					t.Fatalf("valid package rejected: %v %+v", err, items)
				}
				return
			}
			if !errors.Is(err, ErrBadItems) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want ErrBadItems mentioning %q, got %v", c.want, err)
			}
		})
	}
}

// Every problem is listed at once, so the customer fixes the form once.
func TestPackageLinesReportsEveryProblem(t *testing.T) {
	p := box()
	p.Quantity, p.WeightGrams = 0, 0
	_, err := PackageLines([]RequestedPackage{p}, limits)
	if err == nil || !strings.Contains(err.Error(), "quantity") || !strings.Contains(err.Error(), "weight") {
		t.Fatalf("both problems should be reported: %v", err)
	}
}

func TestPackageLinesWithoutTheModule(t *testing.T) {
	if _, err := PackageLines([]RequestedPackage{box()}, nil); !errors.Is(err, ErrBadItems) {
		t.Fatalf("no settings means no packages: %v", err)
	}
}

func TestSizeOf(t *testing.T) {
	items, err := PackageLines([]RequestedPackage{box()}, limits)
	if err != nil {
		t.Fatal(err)
	}
	items = append(items, Item{Kind: LineCatalogue, Quantity: 9}) // no declared size
	s := SizeOf(items)
	// 2 x 1500 g; 330x190x120 mm = 7524 cm3 each.
	if s.WeightGrams != 3000 || s.VolumeCm3 != 2*7524 {
		t.Fatalf("size = %+v", s)
	}
	if got := SizeOf([]Item{{Kind: LinePackage, Quantity: 1, WeightGrams: 1, LengthMm: 1, WidthMm: 1, HeightMm: 1}}); got.VolumeCm3 != 1 {
		t.Fatalf("a tiny parcel still takes some space: %+v", got)
	}
}
