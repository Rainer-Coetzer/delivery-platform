package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
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
	b, err := brand.Load("../../config/brand.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	h := configHandler(b, p, []string{"card", "cash"})
	for _, c := range []struct{ query, acceptLanguage, locale string }{
		{"", "", "en"},
		{"", "de-DE,de;q=0.9", "en"}, // the brand has no German: its default
		{"?locale=EN", "", "en"},
		{"?locale=af", "", "af"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/config"+c.query, nil)
		if c.acceptLanguage != "" {
			req.Header.Set("Accept-Language", c.acceptLanguage)
		}
		h(rec, req)
		var cfg struct {
			packs.PublicConfig
			Brand struct {
				Name     string
				LogoURL  string
				LogoType string
				Currency struct{ Code string }
				Map      struct{ Tiles struct{ URL string } }
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil || rec.Code != 200 {
			t.Fatalf("%d %v", rec.Code, err)
		}
		if cfg.Brand.Name != "Windhoek Eats" || cfg.Brand.LogoURL != "/brand/logo" || cfg.Brand.LogoType != "image/svg+xml" || cfg.Brand.Currency.Code != "NAD" || cfg.Brand.Map.Tiles.URL == "" {
			t.Fatalf("brand: %+v", cfg.Brand)
		}
		if cfg.Locale != c.locale || cfg.Terms["agent"] != "courier" || cfg.Job.Stops != "point_to_point" {
			t.Fatalf("%+v: locale %s, agent %q", c, cfg.Locale, cfg.Terms["agent"])
		}
		if rec.Header().Get("Cache-Control") == "" {
			t.Fatal("config should be cacheable")
		}
	}
}

func TestLogoHandler(t *testing.T) {
	b, err := brand.Load("../../config/brand.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	logoHandler(b)(rec, httptest.NewRequest("GET", "/brand/logo", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}
