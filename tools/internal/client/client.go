// Package client is the tiny JSON-over-HTTP helper shared by tools/seed and
// tools/e2e. Every error carries the method, URL, status and response body,
// because "request failed" is useless when a demo breaks.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// StatusError is returned for any non-2xx response, so callers can accept
// specific statuses (e.g. 409 on re-running seed) without string matching.
type StatusError struct {
	Method, URL string
	Status      int
	Body        string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s -> %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// Do sends body (if non-nil) as JSON and decodes the response into out (if
// non-nil). token, when set, is sent as a Bearer token.
func Do(method, url, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body for %s %s: %w", method, url, err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Method: method, URL: url, Status: resp.StatusCode, Body: string(bytes.TrimSpace(raw))}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode %s %s response %q: %w", method, url, raw, err)
		}
	}
	return nil
}

// Upload POSTs body as-is with contentType (a file, not JSON).
func Upload(url, token, contentType string, body io.Reader) error {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("build POST %s: %w", url, err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Method: http.MethodPost, URL: url, Status: resp.StatusCode, Body: string(bytes.TrimSpace(raw))}
	}
	return nil
}
