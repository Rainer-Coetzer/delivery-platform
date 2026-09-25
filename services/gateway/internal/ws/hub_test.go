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
		{"no jobId", "?token=t", nil, 400},
		{"no token", "?jobId=o1", nil, 401},
		{"bad token", "?jobId=o1&token=t", func(context.Context, string, string) int { return 401 }, 401},
		{"not your job", "?jobId=o1&token=t", func(context.Context, string, string) int { return 403 }, 403},
		{"job not found looks the same as forbidden", "?jobId=o1&token=t", func(context.Context, string, string) int { return 404 }, 403},
		{"job service down", "?jobId=o1&token=t", func(context.Context, string, string) int { return 502 }, 502},
		{"no authorizer configured", "?jobId=o1&token=t", nil, 500},
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

func TestJobAccess(t *testing.T) {
	const secret = "test-secret-that-is-at-least-32-bytes-long"
	issuer := auth.NewIssuer(secret, time.Hour)
	good, _ := issuer.Issue("u1", auth.RoleCustomer, "N", "")

	var sawAuth string
	jobs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/jobs/mine" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(403)
	}))
	defer jobs.Close()

	oa := NewJobAccess(jobs.URL, jobs.Client(), issuer)
	if got := oa.Authorize(context.Background(), "garbage", "mine"); got != 401 {
		t.Fatalf("bad token: %d, want 401 without calling the job service", got)
	}
	if got := oa.Authorize(context.Background(), good, "mine"); got != 200 {
		t.Fatalf("own job: %d, want 200", got)
	}
	if sawAuth != "Bearer "+good {
		t.Fatalf("job service must be called as the user, got Authorization %q", sawAuth)
	}
	if got := oa.Authorize(context.Background(), good, "theirs"); got != 403 {
		t.Fatalf("someone else's job: %d, want 403", got)
	}
}
