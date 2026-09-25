package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// An inter-module call must run the callee's auth as the end user: the
// in-memory hop can't become a way around ADR-0001.
func TestInprocClient_RunsCalleeAuthAsTheUser(t *testing.T) {
	issuer := auth.NewIssuer("test-secret-that-is-at-least-32-bytes-long", time.Hour)
	r := chi.NewRouter()
	r.With(issuer.Middleware, auth.RequireRole(auth.RoleAdmin)).Get("/admin/thing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-User", auth.MustFromContext(r.Context()).UserID)
		_, _ = w.Write([]byte("secret"))
	})
	client := NewInprocClient(r)

	call := func(token string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, InternalBaseURL+"/admin/thing", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	customer, _ := issuer.Issue("u1", auth.RoleCustomer, "C", "")
	admin, _ := issuer.Issue("a1", auth.RoleAdmin, "A", "")

	if got := call("").StatusCode; got != 401 {
		t.Fatalf("no token: %d, want 401", got)
	}
	if got := call(customer).StatusCode; got != 403 {
		t.Fatalf("customer: %d, want 403", got)
	}
	resp := call(admin)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "secret") || resp.Header.Get("X-Seen-User") != "a1" {
		t.Fatalf("admin: %d %q user=%q", resp.StatusCode, body, resp.Header.Get("X-Seen-User"))
	}
}

// Regression: a call made from inside a chi handler (whose context carries
// chi's routing state and the caller's claims) must be routed from scratch,
// and must not see the caller's claims.
func TestInprocClient_FromInsideAHandler(t *testing.T) {
	r := chi.NewRouter()
	client := NewInprocClient(r)
	r.Get("/merchants/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, leaked := auth.FromContext(r.Context()); leaked {
			http.Error(w, "caller claims leaked into callee", 500)
			return
		}
		_, _ = w.Write([]byte("merchant " + chi.URLParam(r, "id")))
	})
	r.Post("/jobs", func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithClaims(r.Context(), &auth.Claims{UserID: "caller"})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, InternalBaseURL+"/merchants/r1", nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
	})

	req, _ := http.NewRequest(http.MethodPost, InternalBaseURL+"/jobs", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "merchant r1" {
		t.Fatalf("nested call: %d %q", resp.StatusCode, body)
	}
}
