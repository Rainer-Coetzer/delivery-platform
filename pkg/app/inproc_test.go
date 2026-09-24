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
