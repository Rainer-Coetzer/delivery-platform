package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

func TestBlockInternal(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := blockInternal(ok)
	for path, want := range map[string]int{"/auth/verify": 404, "/auth/login": 200, "/jobs": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

func TestConfigHandler(t *testing.T) {
	p, err := packs.Load(packfs.FS, "courier", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	h := configHandler(p, []string{"card", "cash"})
	for _, c := range []struct{ query, acceptLanguage, locale string }{
		{"", "", "en"},
		{"", "de-DE,de;q=0.9", "en"}, // courier has no German: English
		{"?locale=EN", "", "en"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/config"+c.query, nil)
		if c.acceptLanguage != "" {
			req.Header.Set("Accept-Language", c.acceptLanguage)
		}
		h(rec, req)
		var cfg packs.PublicConfig
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil || rec.Code != 200 {
			t.Fatalf("%d %v", rec.Code, err)
		}
		if cfg.Locale != c.locale || cfg.Terms["agent"] != "courier" || cfg.Job.Stops != "point_to_point" {
			t.Fatalf("%+v: locale %s, agent %q", c, cfg.Locale, cfg.Terms["agent"])
		}
		if rec.Header().Get("Cache-Control") == "" {
			t.Fatal("config should be cacheable")
		}
	}
}
