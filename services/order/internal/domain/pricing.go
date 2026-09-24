package domain

import (
	"errors"
	"fmt"
)

// ErrBadItems means the requested lines can't be priced: an unknown or
// unavailable item, or a bad quantity. The API maps it to 400 with the
// wrapped message.
var ErrBadItems = errors.New("order items rejected")

// CatalogueItem is one menu entry as the restaurant service reports it.
type CatalogueItem struct {
	ID         string
	Name       string
	PriceCents int64
	Available  bool
}

// RequestedLine is what a customer may choose: which item, how many. Price
// and name are deliberately absent; they come from the catalogue only
// (audit P0-4: the client used to set unitPriceCents).
type RequestedLine struct {
	MenuItemID string
	Quantity   int
}

// maxQuantityPerLine is a sanity bound, not a business rule: it stops an
// int overflow in quantity x price and obviously-fake orders.
const maxQuantityPerLine = 100

// PriceItems turns requested lines into priced order items using only the
// restaurant's catalogue (keyed by menu item id), and returns the subtotal
// in integer cents.
func PriceItems(lines []RequestedLine, catalogue map[string]CatalogueItem) ([]Item, int64, error) {
	if len(lines) == 0 {
		return nil, 0, fmt.Errorf("%w: at least one item is required", ErrBadItems)
	}
	items := make([]Item, 0, len(lines))
	var subtotal int64
	for _, l := range lines {
		if l.Quantity <= 0 || l.Quantity > maxQuantityPerLine {
			return nil, 0, fmt.Errorf("%w: quantity for %s must be 1-%d", ErrBadItems, l.MenuItemID, maxQuantityPerLine)
		}
		c, ok := catalogue[l.MenuItemID]
		if !ok {
			return nil, 0, fmt.Errorf("%w: item %s is not on this restaurant's menu", ErrBadItems, l.MenuItemID)
		}
		if !c.Available {
			return nil, 0, fmt.Errorf("%w: %s is currently unavailable", ErrBadItems, c.Name)
		}
		items = append(items, Item{MenuItemID: c.ID, Name: c.Name, Quantity: l.Quantity, UnitPriceCents: c.PriceCents})
		subtotal += c.PriceCents * int64(l.Quantity)
	}
	return items, subtotal, nil
}
