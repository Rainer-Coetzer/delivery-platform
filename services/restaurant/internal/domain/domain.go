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
	IsOpen   bool          `json:"isOpen"`
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
		IsOpen   bool    `json:"isOpen"`
	}
	return json.Marshal(alias{
		ID: r.ID, Name: r.Name, Lat: r.Lat, Lng: r.Lng,
		OpensAt: formatMinutes(r.OpensAt), ClosesAt: formatMinutes(r.ClosesAt), IsOpen: r.IsOpen,
	})
}

func formatMinutes(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// OpenAt reports whether the restaurant is trading at clock time `at`
// (minutes since midnight). A closesAt earlier than opensAt is treated as
// spanning midnight (e.g. 18:00-02:00).
func (r Restaurant) OpenAt(at time.Duration) bool {
	if !r.IsOpen {
		return false
	}
	if r.ClosesAt > r.OpensAt {
		return at >= r.OpensAt && at < r.ClosesAt
	}
	return at >= r.OpensAt || at < r.ClosesAt
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
}

// CheckStock reports whether every requested item exists, is available and
// has enough stock, given the current catalogue keyed by menu item id. It
// returns the first shortage found, for a useful rejection reason.
func CheckStock(catalogue map[string]MenuItem, requested []RequestedItem) (ok bool, reason string) {
	for _, req := range requested {
		item, found := catalogue[req.MenuItemID]
		if !found {
			return false, "menu item " + req.MenuItemID + " not found"
		}
		if !item.Available {
			return false, item.Name + " is currently unavailable"
		}
		if item.StockQty < req.Quantity {
			return false, item.Name + " is out of stock"
		}
	}
	return true, ""
}
