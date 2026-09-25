// Package brand loads the buyer's brand profile, config/brand.yaml: who
// the platform is (name, logo, colours, font), where it works (currency,
// locales, timezone, map, service area), what it offers (roles, payment
// methods) and which pack it runs, with any pack defaults the buyer
// overrides. It is the buyer's file (ADR-0004); a rebrand is an edit here,
// not a code change.
//
// Like a pack, it is strict (an unknown key is an error), versioned
// (schema), and validated at boot: a mistake stops the server with the
// reason instead of showing up as a wrong colour or a refused payment.
package brand

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rainercoetzer/delivery-platform/pkg/geo"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// SchemaVersion is the brand.yaml format this code reads.
const SchemaVersion = 1

type Brand struct {
	Schema         int            `yaml:"schema" json:"-"`
	Name           string         `yaml:"name" json:"name"`
	Tagline        string         `yaml:"tagline" json:"tagline,omitempty"`
	Logo           string         `yaml:"logo" json:"-"` // a file next to brand.yaml, served at /brand/logo
	Colours        Colours        `yaml:"colours" json:"colours"`
	Font           string         `yaml:"font" json:"font,omitempty"` // a family name; empty means the platform default
	Currency       Currency       `yaml:"currency" json:"currency"`
	Locales        Locales        `yaml:"locales" json:"locales"`
	Timezone       string         `yaml:"timezone" json:"timezone"`
	Map            Map            `yaml:"map" json:"map"`
	ServiceArea    []Area         `yaml:"service_area" json:"serviceArea,omitempty"`
	Roles          []string       `yaml:"roles" json:"-"`           // published through the pack config
	PaymentMethods []string       `yaml:"payment_methods" json:"-"` // likewise
	Pack           string         `yaml:"pack" json:"-"`
	TenantID       string         `yaml:"tenant_id" json:"-"`
	Overrides      PackOverrides  `yaml:"overrides" json:"-"`
	dir            string         // where brand.yaml lives: the logo is relative to it
	loc            *time.Location // Timezone, parsed
}

// Colours are the brand's tokens, as #RRGGBB. Primary is required; the
// rest default from it in the app when left out.
type Colours struct {
	Primary    string `yaml:"primary" json:"primary"`
	Secondary  string `yaml:"secondary" json:"secondary,omitempty"`
	Background string `yaml:"background" json:"background,omitempty"`
	Text       string `yaml:"text" json:"text,omitempty"`
}

// Currency: the ISO 4217 code, how it is written, and how many minor units
// a major one has (2 for NAD and ZAR, 0 for JPY). Money is always stored
// in minor units; this only says how to show it.
type Currency struct {
	Code       string `yaml:"code" json:"code"`
	Symbol     string `yaml:"symbol" json:"symbol"`
	MinorUnits int    `yaml:"minor_units" json:"minorUnits"`
}

type Locales struct {
	Default   string   `yaml:"default" json:"default"`
	Supported []string `yaml:"supported" json:"supported"`
}

type Map struct {
	Centre Point `yaml:"centre" json:"centre"`
	Zoom   int   `yaml:"zoom" json:"zoom"`
	Tiles  Tiles `yaml:"tiles" json:"tiles"`
}

type Point struct {
	Lat float64 `yaml:"lat" json:"lat"`
	Lng float64 `yaml:"lng" json:"lng"`
}

// Tiles is the map tile provider (docs/MAPS.md). URL is a template with
// {z}, {x}, {y} and, if the provider needs one, {key}, filled from the
// environment variable KeyEnv at boot. Tile keys are browser keys: every
// map user sees them, so they are no secret, but they stay out of git.
type Tiles struct {
	URL         string `yaml:"url" json:"url"`
	Attribution string `yaml:"attribution" json:"attribution"`
	KeyEnv      string `yaml:"key_env" json:"-"`
	MaxZoom     int    `yaml:"max_zoom" json:"maxZoom,omitempty"`
}

// Area is part of the service area: a circle. A job every stop of which
// isn't inside some area is refused.
type Area struct {
	Name     string  `yaml:"name" json:"name"`
	Lat      float64 `yaml:"lat" json:"lat"`
	Lng      float64 `yaml:"lng" json:"lng"`
	RadiusKm float64 `yaml:"radius_km" json:"radiusKm"`
}

// PackOverrides are the pack defaults a buyer may change without forking
// the pack: its words and its module settings (a module's settings block
// replaces the pack's whole).
type PackOverrides struct {
	Terminology map[string]map[string]string `yaml:"terminology"`
	Settings    packs.Settings               `yaml:"settings"`
}

var (
	hexColour    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
	localeTag    = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
	logoTypes    = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".svg": "image/svg+xml"}
)

// Load reads and validates the brand file. dev relaxes one rule: the
// public OpenStreetMap tile servers may be used for local development
// (their policy forbids app traffic, docs/MAPS.md).
func Load(path string, dev bool) (*Brand, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("brand file: %w (copy config/brand.yaml, or set BRAND_FILE)", err)
	}
	var b Brand
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("brand file %s: %w", path, err)
	}
	b.dir = filepath.Dir(path)
	if err := b.validate(dev); err != nil {
		return nil, fmt.Errorf("brand file %s is invalid:\n  - %s", path, err)
	}
	// {key} is filled here, once, so a missing key fails the boot rather
	// than every map on every phone.
	if strings.Contains(b.Map.Tiles.URL, "{key}") {
		key := os.Getenv(b.Map.Tiles.KeyEnv)
		if key == "" {
			return nil, fmt.Errorf("brand file %s: map.tiles.url needs a key: set %s", path, b.Map.Tiles.KeyEnv)
		}
		b.Map.Tiles.URL = strings.ReplaceAll(b.Map.Tiles.URL, "{key}", url.QueryEscape(key))
	}
	return &b, nil
}

// PackName reads only the brand file's pack, for tools that need to know
// which pack runs without validating the whole brand.
func PackName(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("brand file: %w", err)
	}
	var only struct {
		Pack string `yaml:"pack"`
	}
	if err := yaml.Unmarshal(raw, &only); err != nil {
		return "", fmt.Errorf("brand file %s: %w", path, err)
	}
	if only.Pack == "" {
		return "", fmt.Errorf("brand file %s names no pack", path)
	}
	return only.Pack, nil
}

func (b *Brand) validate(dev bool) error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if b.Schema != SchemaVersion {
		add("schema %d is not supported (this build reads schema %d)", b.Schema, SchemaVersion)
	}
	if strings.TrimSpace(b.Name) == "" {
		add("name is required")
	}
	if b.Logo != "" {
		if _, ok := logoTypes[strings.ToLower(filepath.Ext(b.Logo))]; !ok {
			add("logo %q: use a .png, .jpg, .webp or .svg file", b.Logo)
		} else if strings.Contains(filepath.ToSlash(b.Logo), "..") || filepath.IsAbs(b.Logo) {
			add("logo %q must be a file inside the config directory", b.Logo)
		} else if _, err := os.Stat(filepath.Join(b.dir, b.Logo)); err != nil {
			add("logo %q: %v", b.Logo, err)
		}
	}
	for name, c := range map[string]string{"primary": b.Colours.Primary, "secondary": b.Colours.Secondary, "background": b.Colours.Background, "text": b.Colours.Text} {
		if c == "" && name != "primary" {
			continue
		}
		if !hexColour.MatchString(c) {
			add("colours.%s %q must be #RRGGBB", name, c)
		}
	}
	if p := b.Colours.contrastProblem(); p != "" {
		add("%s", p)
	}
	if !currencyCode.MatchString(b.Currency.Code) {
		add("currency.code %q must be an ISO 4217 code like NAD or ZAR", b.Currency.Code)
	}
	if b.Currency.Symbol == "" {
		add("currency.symbol is required (N$, R, €)")
	}
	if b.Currency.MinorUnits < 0 || b.Currency.MinorUnits > 3 {
		add("currency.minor_units must be 0-3, got %d", b.Currency.MinorUnits)
	}
	if !localeTag.MatchString(b.Locales.Default) {
		add("locales.default %q must be a language code like en or af", b.Locales.Default)
	}
	if !slices.Contains(b.Locales.Supported, b.Locales.Default) {
		add("locales.supported must include the default %q", b.Locales.Default)
	}
	for _, l := range b.Locales.Supported {
		if !localeTag.MatchString(l) {
			add("locales.supported: %q must be a language code like en or af", l)
		}
	}
	loc, err := time.LoadLocation(b.Timezone)
	if b.Timezone == "" || err != nil {
		add("timezone %q must be an IANA zone like Africa/Windhoek", b.Timezone)
	}
	b.loc = loc
	if !validPoint(b.Map.Centre) {
		add("map.centre must be a real coordinate")
	}
	if b.Map.Zoom < 1 || b.Map.Zoom > 20 {
		add("map.zoom must be 1-20, got %d", b.Map.Zoom)
	}
	problems = append(problems, b.Map.Tiles.problems(dev)...)
	for i, a := range b.ServiceArea {
		if a.Name == "" || a.RadiusKm <= 0 || !validPoint(Point{a.Lat, a.Lng}) {
			add("service_area[%d] needs a name, a real centre and radius_km > 0", i)
		}
	}
	if b.Pack == "" {
		add("pack is required (food, courier, freight, or your own)")
	}
	if b.TenantID == "" {
		b.TenantID = "default"
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// osmPublic are the OpenStreetMap Foundation's own tile servers. Their
// usage policy forbids use by apps like this one; they are allowed only
// for local development.
var osmPublic = []string{"tile.openstreetmap.org", "a.tile.openstreetmap.org", "b.tile.openstreetmap.org", "c.tile.openstreetmap.org"}

func (t Tiles) problems(dev bool) []string {
	var out []string
	u, err := url.Parse(strings.NewReplacer("{z}", "0", "{x}", "0", "{y}", "0", "{s}", "a", "{key}", "k", "{r}", "").Replace(t.URL))
	switch {
	case t.URL == "":
		out = append(out, "map.tiles.url is required (see docs/MAPS.md for providers)")
	case err != nil || u.Scheme != "https" || u.Host == "":
		out = append(out, fmt.Sprintf("map.tiles.url %q must be an https URL template", t.URL))
	case !strings.Contains(t.URL, "{z}") || !strings.Contains(t.URL, "{x}") || !strings.Contains(t.URL, "{y}"):
		out = append(out, "map.tiles.url must have {z}, {x} and {y}")
	case slices.Contains(osmPublic, u.Host) && !dev:
		out = append(out, "map.tiles.url uses OpenStreetMap's public tile servers, whose usage policy forbids app traffic; pick a provider from docs/MAPS.md (allowed only with PROFILE=dev)")
	}
	if t.Attribution == "" {
		out = append(out, "map.tiles.attribution is required: every tile provider's licence asks for it")
	}
	if strings.Contains(t.URL, "{key}") && t.KeyEnv == "" {
		out = append(out, "map.tiles.url has {key}: name the environment variable that holds it in map.tiles.key_env")
	}
	return out
}

// Apply puts the brand's choices onto the pack: its word overrides and
// settings overrides (then re-validated, so an override can't break the
// pack), and checks that the roles and payment methods it enables exist in
// the pack. paymentMethods are the methods the pack's providers take.
func (b *Brand) Apply(p *packs.Pack, paymentMethods []string) error {
	if p.Name != b.Pack {
		// PACK in the environment chose another pack (tests, the e2e).
		// The brand's overrides are for its own pack only.
		return nil
	}
	for locale, words := range b.Overrides.Terminology {
		if p.Terminology[locale] == nil {
			p.Terminology[locale] = map[string]string{}
		}
		for k, v := range words {
			p.Terminology[locale][k] = v
		}
	}
	o := b.Overrides.Settings
	if o.Packages != nil {
		p.Settings.Packages = o.Packages
	}
	if o.MultiStop != nil {
		p.Settings.MultiStop = o.MultiStop
	}
	if o.Scheduling != nil {
		p.Settings.Scheduling = o.Scheduling
	}
	if o.Proof != nil {
		p.Settings.Proof = o.Proof
	}
	if o.Documents != nil {
		p.Settings.Documents = o.Documents
	}
	if err := p.Revalidate(); err != nil {
		return fmt.Errorf("brand overrides for pack %s: %w", p.Name, err)
	}
	for _, r := range b.Roles {
		if !slices.Contains(p.Roles, r) {
			return fmt.Errorf("brand roles: %q isn't a role pack %s has (%v)", r, p.Name, p.Roles)
		}
	}
	for _, m := range b.PaymentMethods {
		if !slices.Contains(paymentMethods, m) {
			return fmt.Errorf("brand payment_methods: %q isn't a method pack %s takes (%v)", m, p.Name, paymentMethods)
		}
	}
	return nil
}

// EnabledRoles is the brand's roles, or all the pack's when it names none.
func (b *Brand) EnabledRoles(p *packs.Pack) []string {
	if len(b.Roles) == 0 || p.Name != b.Pack {
		return p.Roles
	}
	return b.Roles
}

// EnabledPaymentMethods is the brand's methods, or all the pack's.
func (b *Brand) EnabledPaymentMethods(p *packs.Pack, all []string) []string {
	if len(b.PaymentMethods) == 0 || p.Name != b.Pack {
		return all
	}
	return b.PaymentMethods
}

// InServiceArea reports whether a point is inside any service area. No
// areas means everywhere: a brand that hasn't drawn one serves anywhere.
func (b *Brand) InServiceArea(lat, lng float64) bool {
	if len(b.ServiceArea) == 0 {
		return true
	}
	for _, a := range b.ServiceArea {
		if geo.HaversineMeters(lat, lng, a.Lat, a.Lng) <= a.RadiusKm*1000 {
			return true
		}
	}
	return false
}

// Location is the brand's timezone.
func (b *Brand) Location() *time.Location { return b.loc }

// LogoFile is the logo's path on disk and its content type, or "" when the
// brand has none.
func (b *Brand) LogoFile() (path, contentType string) {
	if b.Logo == "" {
		return "", ""
	}
	return filepath.Join(b.dir, b.Logo), logoTypes[strings.ToLower(filepath.Ext(b.Logo))]
}

// FormatMoney writes minor units in the brand's currency: 4300 -> "N$43.00".
func (b *Brand) FormatMoney(minor int64) string {
	return FormatMoney(minor, b.Currency)
}

// ParseMoney reads a typed amount ("65", "65.5", "65,50") as minor units
// of c. It is the app's Currency.parse (lib/core/brand.dart), for imports:
// text in, integers only, never a float, so 0.29 can't become 28. It
// refuses a sign, more decimals than the currency has, or anything else.
func ParseMoney(s string, c Currency) (int64, error) {
	m := moneyText.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("%q is not an amount like 12.50", s)
	}
	whole, fraction := m[1], m[2]
	if len(fraction) > c.MinorUnits {
		return 0, fmt.Errorf("%q has more decimals than %s has (%d)", s, c.Code, c.MinorUnits)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is too large", s)
	}
	scale := int64(1)
	for i := 0; i < c.MinorUnits; i++ {
		scale *= 10
	}
	var f int64
	if fraction != "" {
		f, _ = strconv.ParseInt(fraction+strings.Repeat("0", c.MinorUnits-len(fraction)), 10, 64)
	}
	if w > (math.MaxInt64-f)/scale {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return w*scale + f, nil
}

var moneyText = regexp.MustCompile(`^(\d+)(?:[.,](\d*))?$`)

func FormatMoney(minor int64, c Currency) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	out := c.Symbol
	if c.MinorUnits == 0 {
		out += fmt.Sprintf("%d", minor)
	} else {
		div := int64(1)
		for i := 0; i < c.MinorUnits; i++ {
			div *= 10
		}
		out += fmt.Sprintf("%d.%0*d", minor/div, c.MinorUnits, minor%div)
	}
	if neg {
		return "-" + out
	}
	return out
}

func validPoint(p Point) bool {
	return !(p.Lat == 0 && p.Lng == 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}
