package brand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	packfs "github.com/rainercoetzer/delivery-platform/packs"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// The shipped brand file must load (in dev: it uses OpenStreetMap tiles).
func TestShippedBrandLoads(t *testing.T) {
	b, err := Load("../../config/brand.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name == "" || b.Pack != "food" || b.TenantID != "default" || b.Location().String() != "Africa/Windhoek" {
		t.Fatalf("%+v", b)
	}
	if path, ct := b.LogoFile(); ct != "image/svg+xml" || !strings.HasSuffix(path, "logo.svg") {
		t.Fatalf("logo: %s %s", path, ct)
	}
	// Outside dev, OpenStreetMap's public tiles are refused.
	if _, err := Load("../../config/brand.yaml", false); err == nil || !strings.Contains(err.Error(), "usage policy") {
		t.Fatalf("OSM tiles outside dev must be refused: %v", err)
	}
}

const good = `schema: 1
name: Test Couriers
colours: { primary: "#112233" }
currency: { code: ZAR, symbol: "R", minor_units: 2 }
locales: { default: en, supported: [en, af] }
timezone: Africa/Johannesburg
map:
  centre: { lat: -26.2, lng: 28.04 }
  zoom: 12
  tiles: { url: "https://tiles.example.com/{z}/{x}/{y}.png?key={key}", attribution: "© Example", key_env: TEST_TILE_KEY }
service_area:
  - { name: Joburg, lat: -26.2, lng: 28.04, radius_km: 40 }
pack: courier
`

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brand.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	t.Setenv("TEST_TILE_KEY", "abc 123")
	b, err := Load(write(t, good), false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Map.Tiles.URL != "https://tiles.example.com/{z}/{x}/{y}.png?key=abc+123" {
		t.Fatalf("the key is filled in, escaped: %s", b.Map.Tiles.URL)
	}
	if !b.InServiceArea(-26.1, 28.0) || b.InServiceArea(-22.57, 17.08) {
		t.Fatal("Joburg is in the area, Windhoek isn't")
	}
	if got := b.FormatMoney(4300); got != "R43.00" {
		t.Fatalf("money: %s", got)
	}
}

func TestLoadNamesEveryProblem(t *testing.T) {
	t.Setenv("TEST_TILE_KEY", "k")
	cases := []struct{ name, from, to, want string }{
		{"typo", "timezone:", "timezon:", "field timezon not found"},
		{"schema", "schema: 1", "schema: 2", "schema 2 is not supported"},
		{"colour", `"#112233"`, `"red"`, "colours.primary"},
		{"currency", "code: ZAR", "code: rand", "ISO 4217"},
		{"minor units", "minor_units: 2", "minor_units: 9", "minor_units must be 0-3"},
		{"default locale not supported", "default: en", "default: de", "must include the default"},
		{"timezone", "Africa/Johannesburg", "Mars/Olympus", "IANA zone"},
		{"zoom", "zoom: 12", "zoom: 40", "map.zoom"},
		{"http tiles", "https://tiles.example.com", "http://tiles.example.com", "https URL template"},
		{"no attribution", `attribution: "© Example", `, "", "attribution is required"},
		{"key without env", ", key_env: TEST_TILE_KEY", "", "name the environment variable"},
		{"area", "radius_km: 40", "radius_km: 0", "service_area[0]"},
		{"pack", "pack: courier", "pack: \"\"", "pack is required"},
		{"logo type", "pack: courier", "pack: courier\nlogo: logo.gif", "use a .png"},
		{"logo escape", "pack: courier", "pack: courier\nlogo: ../secret.png", "inside the config directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, strings.Replace(good, c.from, c.to, 1)), false)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
	t.Setenv("TEST_TILE_KEY", "")
	if _, err := Load(write(t, good), false); err == nil || !strings.Contains(err.Error(), "set TEST_TILE_KEY") {
		t.Fatalf("a missing tile key fails the boot: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), false); err == nil || !strings.Contains(err.Error(), "BRAND_FILE") {
		t.Fatalf("a missing file says how to fix it: %v", err)
	}
}

func courier(t *testing.T) *packs.Pack {
	t.Helper()
	p, err := packs.Load(packfs.FS, "courier", lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApply(t *testing.T) {
	t.Setenv("TEST_TILE_KEY", "k")
	b, err := Load(write(t, good+`roles: [customer, agent]
payment_methods: [cash]
overrides:
  terminology: { en: { agent: rider } }
  settings:
    packages: { max_lines: 3, max_quantity: 2, max_weight_g: 5000, max_dimension_mm: 600, max_declared_value_cents: 100000 }
`), false)
	if err != nil {
		t.Fatal(err)
	}
	p := courier(t)
	if err := b.Apply(p, []string{"card", "cash"}); err != nil {
		t.Fatal(err)
	}
	if p.Term("en", "agent") != "rider" || p.Term("en", "job") != "delivery" || p.Settings.Packages.MaxWeightGrams != 5000 {
		t.Fatalf("overrides: %v %+v", p.Terminology["en"], p.Settings.Packages)
	}
	if got := b.EnabledRoles(p); strings.Join(got, ",") != "customer,agent" {
		t.Fatalf("roles: %v", got)
	}
	if got := b.EnabledPaymentMethods(p, []string{"card", "cash"}); strings.Join(got, ",") != "cash" {
		t.Fatalf("payment methods: %v", got)
	}
}

func TestApplyRefusesWhatThePackCantDo(t *testing.T) {
	t.Setenv("TEST_TILE_KEY", "k")
	for _, c := range []struct{ extra, want string }{
		{"roles: [customer, pilot]\n", `"pilot" isn't a role`},
		{"payment_methods: [bitcoin]\n", `"bitcoin" isn't a method`},
		{"overrides:\n  settings:\n    packages: { max_lines: 0, max_quantity: 1, max_weight_g: 1, max_dimension_mm: 1, max_declared_value_cents: 1 }\n", "max_lines must be > 0"},
		{"overrides:\n  settings:\n    multi-stop: { max_stops: 5 }\n", "multi-stop module isn't enabled"},
	} {
		b, err := Load(write(t, good+c.extra), false)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Apply(courier(t), []string{"card", "cash"}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.extra, c.want, err)
		}
	}
}

// PACK in the environment picked another pack: the brand's overrides are
// for its own pack, so they are left alone rather than misapplied.
func TestApplyToAnotherPackIsANoop(t *testing.T) {
	t.Setenv("TEST_TILE_KEY", "k")
	b, err := Load(write(t, strings.Replace(good, "pack: courier", "pack: freight", 1)+"roles: [customer]\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	p := courier(t)
	if err := b.Apply(p, nil); err != nil || len(b.EnabledRoles(p)) != len(p.Roles) {
		t.Fatalf("%v %v", err, b.EnabledRoles(p))
	}
}

func TestFormatMoney(t *testing.T) {
	for _, c := range []struct {
		minor int64
		cur   Currency
		want  string
	}{
		{4300, Currency{Symbol: "N$", MinorUnits: 2}, "N$43.00"},
		{5, Currency{Symbol: "N$", MinorUnits: 2}, "N$0.05"},
		{-250, Currency{Symbol: "R", MinorUnits: 2}, "-R2.50"},
		{1500, Currency{Symbol: "¥", MinorUnits: 0}, "¥1500"},
		{1234, Currency{Symbol: "BD", MinorUnits: 3}, "BD1.234"},
	} {
		if got := FormatMoney(c.minor, c.cur); got != c.want {
			t.Errorf("%d: %s, want %s", c.minor, got, c.want)
		}
	}
}

// Tools read only the pack's name, without validating the brand (whose OSM
// tiles they'd refuse outside dev).
func TestPackName(t *testing.T) {
	if got, err := PackName("../../config/brand.yaml"); err != nil || got != "food" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := PackName(write(t, "name: x\n")); err == nil || !strings.Contains(err.Error(), "names no pack") {
		t.Fatalf("no pack: %v", err)
	}
	if _, err := PackName(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file must fail")
	}
}
