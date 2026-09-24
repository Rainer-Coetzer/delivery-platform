// Package domain holds the restaurant-side business rules: whether a
// restaurant can accept an incoming order right now, kept separate from
// storage and transport so it is easy to unit test.
package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Restaurant is returned directly by the REST API. OpensAt/ClosesAt are
// excluded from the default json tags because encoding/json would otherwise
// render a time.Duration as its raw nanosecond count; MarshalJSON below
// renders them as "HH:MM" instead, matching the format the create endpoint
// accepts.
type Restaurant struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Lat      float64       `json:"lat"`
	Lng      float64       `json:"lng"`
	OpensAt  time.Duration `json:"-"`
	ClosesAt time.Duration `json:"-"`
	// Timezone is an IANA zone ("Africa/Windhoek"). OpensAt/ClosesAt are
	// wall-clock times in this zone.
	Timezone string `json:"timezone"`
	IsOpen   bool   `json:"isOpen"`
}

// MarshalJSON renders OpensAt/ClosesAt as "HH:MM" strings, matching the
// format the REST API accepts them in on create.
func (r Restaurant) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
		OpensAt  string  `json:"opensAt"`
		ClosesAt string  `json:"closesAt"`
		Timezone string  `json:"timezone"`
		IsOpen   bool    `json:"isOpen"`
	}
	return json.Marshal(alias{
		ID: r.ID, Name: r.Name, Lat: r.Lat, Lng: r.Lng,
		OpensAt: formatMinutes(r.OpensAt), ClosesAt: formatMinutes(r.ClosesAt),
		Timezone: r.Timezone, IsOpen: r.IsOpen,
	})
}

func formatMinutes(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// OpenAt reports whether the restaurant is trading at wall-clock time `at`
// (time since local midnight). A closesAt earlier than opensAt is treated as
// spanning midnight (e.g. 18:00-02:00); equal times mean open all day.
func (r Restaurant) OpenAt(at time.Duration) bool {
	if !r.IsOpen {
		return false
	}
	if r.ClosesAt > r.OpensAt {
		return at >= r.OpensAt && at < r.ClosesAt
	}
	return at >= r.OpensAt || at < r.ClosesAt
}

// OpenAtTime converts the instant t into the restaurant's own timezone and
// checks the hours there. Timestamps are UTC everywhere else in the system;
// this is the one place wall-clock time matters.
//
// Trap: loading a zone needs tz data. The Alpine runtime image has none,
// so the restaurant binary embeds it with `import _ "time/tzdata"` in
// cmd/main.go. Without that, every LoadLocation fails in the container
// while passing on a dev machine.
func (r Restaurant) OpenAtTime(t time.Time) (bool, error) {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return false, fmt.Errorf("restaurant %s has invalid timezone %q: %w", r.ID, r.Timezone, err)
	}
	local := t.In(loc)
	return r.OpenAt(time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute), nil
}

type MenuItem struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurantId"`
	Name         string `json:"name"`
	PriceCents   int64  `json:"priceCents"`
	StockQty     int    `json:"stockQty"`
	Available    bool   `json:"available"`
}

type RequestedItem struct {
	MenuItemID string
	Quantity   int
	// UnitPriceCents is the price the Order service charged. The Order
	// service prices from this service's catalogue, so a mismatch means the
	// price changed between pricing and acceptance, and the customer must not
	// be charged a stale amount.
	UnitPriceCents int64
}

// CheckItems reports whether every requested item is on restaurantID's own
// menu, available, priced as charged and in stock, given the catalogue
// keyed by menu item id. It returns the first problem as the rejection
// reason. Before this, items from another restaurant or any client-chosen
// price were accepted (audit P0-4).
func CheckItems(restaurantID string, catalogue map[string]MenuItem, requested []RequestedItem) (ok bool, reason string) {
	if len(requested) == 0 {
		return false, "order has no items"
	}
	for _, req := range requested {
		item, found := catalogue[req.MenuItemID]
		if !found || item.RestaurantID != restaurantID {
			return false, "menu item " + req.MenuItemID + " is not on this restaurant's menu"
		}
		if req.Quantity <= 0 {
			return false, "quantity for " + item.Name + " must be positive"
		}
		if !item.Available {
			return false, item.Name + " is currently unavailable"
		}
		if item.PriceCents != req.UnitPriceCents {
			return false, fmt.Sprintf("price of %s changed (charged %d, now %d cents)", item.Name, req.UnitPriceCents, item.PriceCents)
		}
		if item.StockQty < req.Quantity {
			return false, item.Name + " is out of stock"
		}
	}
	return true, ""
}
