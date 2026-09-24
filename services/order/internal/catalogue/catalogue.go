// Package catalogue reads a restaurant and its menu from the Restaurant
// service so the Order service can price orders itself.
//
// This is a synchronous call on the order-creation path, chosen over
// replicating menus into order_db via events: prices must be current at the
// moment of ordering, and the restaurant re-checks them on acceptance
// anyway (domain.CheckItems there), so a replicated copy would add a
// staleness window without removing the call's failure mode.
//
// Calls are made as the end user, forwarding their Authorization header
// (docs/adr/0001), so the restaurant service applies its own authz.
package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
)

var ErrRestaurantNotFound = errors.New("restaurant not found")

type Restaurant struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	IsOpen bool    `json:"isOpen"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Fetch returns the restaurant and its menu keyed by item id.
func (c *Client) Fetch(ctx context.Context, authorization, restaurantID string) (*Restaurant, map[string]domain.CatalogueItem, error) {
	// restaurantID is client input: escape it so it can only ever name one
	// path segment on the restaurant service.
	id := url.PathEscape(restaurantID)
	var r Restaurant
	if err := c.get(ctx, authorization, "/restaurants/"+id, &r); err != nil {
		return nil, nil, err
	}
	var menu []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PriceCents int64  `json:"priceCents"`
		Available  bool   `json:"available"`
	}
	if err := c.get(ctx, authorization, "/restaurants/"+id+"/menu-items", &menu); err != nil {
		return nil, nil, err
	}
	out := make(map[string]domain.CatalogueItem, len(menu))
	for _, m := range menu {
		out[m.ID] = domain.CatalogueItem{ID: m.ID, Name: m.Name, PriceCents: m.PriceCents, Available: m.Available}
	}
	return &r, out, nil
}

func (c *Client) get(ctx context.Context, authorization, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authorization)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("restaurant service GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrRestaurantNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("restaurant service GET %s -> %d: %s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode restaurant service GET %s: %w", path, err)
	}
	return nil
}
