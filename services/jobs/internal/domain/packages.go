package domain

import (
	"fmt"
	"strings"

	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// Line kinds.
const (
	LineCatalogue = "catalogue"
	LinePackage   = "package"
)

// RequestedPackage is a freeform line a customer declares (the packages
// module): what it is and how big, not what it costs. Weight and
// dimensions drive pricing and which vehicle can take it.
type RequestedPackage struct {
	Description        string `json:"description"`
	Quantity           int    `json:"quantity"`
	WeightGrams        int64  `json:"weightGrams"`
	LengthMm           int    `json:"lengthMm"`
	WidthMm            int    `json:"widthMm"`
	HeightMm           int    `json:"heightMm"`
	DeclaredValueCents int64  `json:"declaredValueCents"`
}

// maxDescription is a storage sanity bound, not a business rule.
const maxDescription = 200

// PackageLines validates requested packages against the pack's limits and
// returns them as job lines. Every problem is reported, not just the
// first, so a customer fixes the form in one go.
func PackageLines(req []RequestedPackage, limits *packs.PackagesSettings) ([]Item, error) {
	if limits == nil {
		return nil, fmt.Errorf("%w: this platform doesn't take packages", ErrBadItems)
	}
	if len(req) == 0 {
		return nil, fmt.Errorf("%w: at least one package is required", ErrBadItems)
	}
	var problems []string
	if len(req) > limits.MaxLines {
		problems = append(problems, fmt.Sprintf("at most %d package lines", limits.MaxLines))
	}
	out := make([]Item, 0, len(req))
	for i, p := range req {
		n := i + 1
		desc := strings.TrimSpace(p.Description)
		switch {
		case desc == "":
			problems = append(problems, fmt.Sprintf("package %d needs a description", n))
		case len(desc) > maxDescription:
			problems = append(problems, fmt.Sprintf("package %d: description is longer than %d characters", n, maxDescription))
		}
		if p.Quantity < 1 || p.Quantity > limits.MaxQuantity {
			problems = append(problems, fmt.Sprintf("package %d: quantity must be 1-%d", n, limits.MaxQuantity))
		}
		if p.WeightGrams < 1 || p.WeightGrams > limits.MaxWeightGrams {
			problems = append(problems, fmt.Sprintf("package %d: weight must be 1-%d g", n, limits.MaxWeightGrams))
		}
		for _, d := range []struct {
			name string
			mm   int
		}{{"length", p.LengthMm}, {"width", p.WidthMm}, {"height", p.HeightMm}} {
			if d.mm < 1 || d.mm > limits.MaxDimensionMm {
				problems = append(problems, fmt.Sprintf("package %d: %s must be 1-%d mm", n, d.name, limits.MaxDimensionMm))
			}
		}
		if p.DeclaredValueCents < 0 || p.DeclaredValueCents > limits.MaxDeclaredValueCents {
			problems = append(problems, fmt.Sprintf("package %d: declared value must be 0-%d cents", n, limits.MaxDeclaredValueCents))
		}
		out = append(out, Item{
			Kind: LinePackage, Name: desc, Quantity: p.Quantity, WeightGrams: p.WeightGrams,
			LengthMm: p.LengthMm, WidthMm: p.WidthMm, HeightMm: p.HeightMm, DeclaredValueCents: p.DeclaredValueCents,
		})
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrBadItems, strings.Join(problems, "; "))
	}
	return out, nil
}

// Size is what a job's lines add up to, for pricing and vehicle capacity.
type Size struct {
	WeightGrams int64
	VolumeCm3   int64
}

// SizeOf totals the package lines. Catalogue lines have no declared size
// and count as nothing: food isn't dispatched by capacity.
func SizeOf(items []Item) Size {
	var s Size
	for _, it := range items {
		if it.Kind != LinePackage {
			continue
		}
		q := int64(it.Quantity)
		s.WeightGrams += q * it.WeightGrams
		// mm^3 to cm^3, rounded up so a small parcel is never "0 cm3".
		mm3 := int64(it.LengthMm) * int64(it.WidthMm) * int64(it.HeightMm)
		s.VolumeCm3 += q * ((mm3 + 999) / 1000)
	}
	return s
}
