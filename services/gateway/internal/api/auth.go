// Package api holds the Gateway's own handlers: login (which the reverse
// proxy can't do, since it has to turn a customer-service response into a
// signed JWT) and a couple of small conveniences.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/httpx"
)

type AuthAPI struct {
	CustomerServiceURL string
	Issuer             *auth.Issuer
	Log                *slog.Logger
	httpClient         *http.Client
}

// NewAuthAPI calls the customer module through client: the network between
// microservices, in-memory inside cmd/platform.
func NewAuthAPI(customerServiceURL string, client *http.Client, issuer *auth.Issuer, log *slog.Logger) *AuthAPI {
	return &AuthAPI{CustomerServiceURL: customerServiceURL, Issuer: issuer, Log: log, httpClient: client}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	RefID string `json:"refId"`
}

// Login verifies credentials against the customer service (which owns all
// accounts, regardless of role) and, on success, signs a JWT the client
// attaches as a Bearer token to every subsequent request.
func (a *AuthAPI) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.BadRequest(w, "invalid body: "+err.Error())
		return
	}

	profile, status, err := a.verify(r.Context(), req)
	if err != nil {
		a.Log.Error("verify credentials", "error", err)
		httpx.Internal(w, "could not verify credentials")
		return
	}
	if status != http.StatusOK {
		httpx.Unauthorized(w, "invalid email or password")
		return
	}

	token, err := a.Issuer.Issue(profile.ID, auth.Role(profile.Role), profile.Name, profile.RefID)
	if err != nil {
		a.Log.Error("issue token", "error", err)
		httpx.Internal(w, "could not issue token")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"token": token, "user": profile})
}

func (a *AuthAPI) verify(ctx context.Context, req loginRequest) (*userProfile, int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.CustomerServiceURL+"/auth/verify", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("call customer service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	var profile userProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, 0, err
	}
	return &profile, http.StatusOK, nil
}

var ErrUpstream = errors.New("upstream error")
