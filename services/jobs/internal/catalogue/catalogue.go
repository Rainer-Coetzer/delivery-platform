// Package catalogue reads a merchant and its catalogue from the Merchant
// service so the jobs module can price jobs itself.
//
// This is a synchronous call on the job-creation path, chosen over
// replicating catalogues into jobs_db via events: prices must be current at the
// moment of ordering, and the merchant re-checks them on acceptance
// anyway (domain.CheckItems there), so a replicated copy would add a
// staleness window without removing the call's failure mode.
//
// Calls are made as the end user, forwarding their Authorization header
// (docs/adr/0001), so the merchant service applies its own authz.
package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
)

var ErrMerchantNotFound = errors.New("merchant not found")

type Merchant struct {
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

// New uses client for the calls: a network client between microservices,
// the in-memory one inside cmd/platform (pkg/app).
func New(baseURL string, client *http.Client) *Client {
	return &Client{BaseURL: baseURL, HTTP: client}
}

// Fetch returns the merchant and its catalogue keyed by item id.
func (c *Client) Fetch(ctx context.Context, authorization, merchantID string) (*Merchant, map[string]domain.CatalogueItem, error) {
	// merchantID is client input: escape it so it can only ever name one
	// path segment on the merchant service.
	id := url.PathEscape(merchantID)
	var r Merchant
	if err := c.get(ctx, authorization, "/merchants/"+id, &r); err != nil {
		return nil, nil, err
	}
	var catalogue []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PriceCents int64  `json:"priceCents"`
		Available  bool   `json:"available"`
	}
	if err := c.get(ctx, authorization, "/merchants/"+id+"/catalogue-items", &catalogue); err != nil {
		return nil, nil, err
	}
	out := make(map[string]domain.CatalogueItem, len(catalogue))
	for _, m := range catalogue {
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
		return fmt.Errorf("merchant service GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrMerchantNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("merchant service GET %s -> %d: %s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode merchant service GET %s: %w", path, err)
	}
	return nil
}
