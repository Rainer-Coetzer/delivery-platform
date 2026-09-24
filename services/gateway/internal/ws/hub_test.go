package ws

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/auth"
)

// The hub must refuse before upgrading; a refused handshake is a plain HTTP
// response, so httptest can check it without a WebSocket client.
func TestHubRefusesBeforeUpgrade(t *testing.T) {
	cases := []struct {
		name  string
		query string
		authz func(context.Context, string, string) int
		want  int
	}{
		{"no orderId", "?token=t", nil, 400},
		{"no token", "?orderId=o1", nil, 401},
		{"bad token", "?orderId=o1&token=t", func(context.Context, string, string) int { return 401 }, 401},
		{"not your order", "?orderId=o1&token=t", func(context.Context, string, string) int { return 403 }, 403},
		{"order not found looks the same as forbidden", "?orderId=o1&token=t", func(context.Context, string, string) int { return 404 }, 403},
		{"order service down", "?orderId=o1&token=t", func(context.Context, string, string) int { return 502 }, 502},
		{"no authorizer configured", "?orderId=o1&token=t", nil, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.Authorize = c.authz
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws"+c.query, nil))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestOrderAccess(t *testing.T) {
	const secret = "test-secret-that-is-at-least-32-bytes-long"
	issuer := auth.NewIssuer(secret, time.Hour)
	good, _ := issuer.Issue("u1", auth.RoleCustomer, "N", "")

	var sawAuth string
	orders := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/orders/mine" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(403)
	}))
	defer orders.Close()

	oa := NewOrderAccess(orders.URL, issuer)
	if got := oa.Authorize(context.Background(), "garbage", "mine"); got != 401 {
		t.Fatalf("bad token: %d, want 401 without calling the order service", got)
	}
	if got := oa.Authorize(context.Background(), good, "mine"); got != 200 {
		t.Fatalf("own order: %d, want 200", got)
	}
	if sawAuth != "Bearer "+good {
		t.Fatalf("order service must be called as the user, got Authorization %q", sawAuth)
	}
	if got := oa.Authorize(context.Background(), good, "theirs"); got != 403 {
		t.Fatalf("someone else's order: %d, want 403", got)
	}
}
