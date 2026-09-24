package domain

import (
	"errors"
	"testing"
)

func TestPriceItems(t *testing.T) {
	catalogue := map[string]CatalogueItem{
		"a": {ID: "a", Name: "Burger", PriceCents: 8500, Available: true},
		"b": {ID: "b", Name: "Chips", PriceCents: 2050, Available: true},
		"c": {ID: "c", Name: "Soup", PriceCents: 3000, Available: false},
	}
	cases := []struct {
		name     string
		lines    []RequestedLine
		wantSub  int64
		wantErr  bool
		firstNam string
	}{
		{"single line", []RequestedLine{{"a", 2}}, 17000, false, "Burger"},
		{"two lines, integer cents", []RequestedLine{{"a", 1}, {"b", 3}}, 8500 + 3*2050, false, "Burger"},
		{"unknown item", []RequestedLine{{"zzz", 1}}, 0, true, ""},
		{"unavailable", []RequestedLine{{"c", 1}}, 0, true, ""},
		{"zero quantity", []RequestedLine{{"a", 0}}, 0, true, ""},
		{"negative quantity", []RequestedLine{{"a", -3}}, 0, true, ""},
		{"absurd quantity", []RequestedLine{{"a", 1_000_000}}, 0, true, ""},
		{"no lines", nil, 0, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, sub, err := PriceItems(c.lines, catalogue)
			if c.wantErr {
				if !errors.Is(err, ErrBadItems) {
					t.Fatalf("err = %v, want ErrBadItems", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sub != c.wantSub {
				t.Fatalf("subtotal = %d, want %d", sub, c.wantSub)
			}
			// Name and price must come from the catalogue, never the request.
			if items[0].Name != c.firstNam || items[0].UnitPriceCents != catalogue[c.lines[0].MenuItemID].PriceCents {
				t.Fatalf("item not priced from catalogue: %+v", items[0])
			}
		})
	}
}
