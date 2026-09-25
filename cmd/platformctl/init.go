package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/brand"
)

// stdin is where init reads answers; tests replace it.
var stdin io.Reader = os.Stdin

// runInit writes config/brand.yaml and .env from a few answers: the
// brand, the pack, the currency, where the platform works and the first
// admin. Each question has a flag, so a script can answer them all and
// pass --yes. Neither file is overwritten without --force, and then the
// old one is kept as <file>.bak.
func runInit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(out)
	a := initAnswers{}
	fs.StringVar(&a.name, "name", "", "the platform's name, shown in the app")
	fs.StringVar(&a.pack, "pack", "", "the pack: food, courier, freight, or yours")
	fs.StringVar(&a.colour, "colour", "", "the primary colour, #RRGGBB")
	fs.StringVar(&a.currency, "currency", "", "ISO 4217 code, e.g. ZAR")
	fs.StringVar(&a.symbol, "symbol", "", "how amounts are written, e.g. R")
	fs.StringVar(&a.minorUnits, "minor-units", "", "decimals the currency has (2 for most)")
	fs.StringVar(&a.timezone, "timezone", "", "IANA zone, e.g. Africa/Johannesburg")
	fs.StringVar(&a.centre, "centre", "", "where maps open, as lat,lng")
	fs.StringVar(&a.locale, "locale", "", "the default language: en or af")
	fs.StringVar(&a.adminEmail, "admin-email", "", "the first admin's email")
	fs.StringVar(&a.origin, "origin", "", "the app's web address, e.g. https://app.example.com (production only)")
	prod := fs.Bool("prod", false, "set up for production: PROFILE=prod, and every production rule applies")
	yes := fs.Bool("yes", false, "don't ask: take flags and defaults, and fail on anything missing")
	force := fs.Bool("force", false, "replace existing files (keeping each as <file>.bak)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	brandPath := filepath.Join("config", "brand.yaml")
	for _, p := range []string{brandPath, ".env"} {
		if _, err := os.Stat(p); err == nil && !*force {
			return fmt.Errorf("%s exists; run with --force to replace it (the old one is kept as %s.bak)", p, p)
		}
	}

	q := &asker{in: bufio.NewReader(stdin), out: out, ask: !*yes}
	fmt.Fprintln(out, "Setting up the platform. Enter keeps the value in [brackets].")
	a.name = q.value("Platform name", a.name, "", nonEmpty)
	a.pack = q.value("Pack (food, courier, freight, or yours in packs/)", a.pack, "courier", packExists)
	a.colour = q.value("Primary colour", a.colour, "#1F6FEB", matches(`^#[0-9A-Fa-f]{6}$`, "#RRGGBB"))
	a.currency = strings.ToUpper(q.value("Currency (ISO 4217 code)", a.currency, "", matches(`^[A-Za-z]{3}$`, "three letters, e.g. ZAR")))
	a.symbol = q.value("Currency symbol", a.symbol, "", nonEmpty)
	a.minorUnits = q.value("Decimals in the currency", a.minorUnits, "2", matches(`^[0-3]$`, "0 to 3"))
	a.timezone = q.value("Timezone (IANA)", a.timezone, "", zone)
	a.centre = q.value("Map centre (lat,lng)", a.centre, "", latLng)
	a.locale = q.value("Default language (en or af)", a.locale, "en", matches(`^(en|af)$`, "en or af"))
	a.adminEmail = q.value("First admin's email", a.adminEmail, "", matches(`^[^@\s]+@[^@\s]+\.[^@\s]+$`, "an email address"))
	if *prod {
		a.origin = q.value("The app's web address", a.origin, "", matches(`^https://[^\s,*]+$`, "an https:// address"))
	}
	if q.err != nil {
		return q.err
	}

	password, err := randomSecret(18)
	if err != nil {
		return err
	}
	secret, err := randomSecret(48)
	if err != nil {
		return err
	}
	brandYAML := a.brandYAML()
	envText, err := a.envFile(*prod, secret, password)
	if err != nil {
		return err
	}
	// Prove the brand loads before anything is written: init never leaves
	// a file the server would refuse.
	if err := checkBrand(brandYAML); err != nil {
		return fmt.Errorf("the brand file init would write doesn't load (a bug in init; nothing was written): %w", err)
	}
	for _, f := range []struct{ path, text string }{{brandPath, brandYAML}, {".env", envText}} {
		if err := writeKeepingOld(f.path, f.text, *force); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", f.path)
	}
	fmt.Fprintf(out, "\nThe first admin is %s, password %s\n", a.adminEmail, password)
	fmt.Fprintln(out, "It is in .env (BOOTSTRAP_ADMIN_PASSWORD) and shown only here. Change it after the first sign-in.")
	if *prod {
		fmt.Fprintln(out, "\nProduction: map tiles are OpenStreetMap's for now, which the server refuses outside dev.")
		fmt.Fprintln(out, "Choose a tile provider (docs/MAPS.md) and set map.tiles in config/brand.yaml.")
	}
	fmt.Fprint(out, `
Next:
  platformctl doctor    checks the tools and these files
  docker compose --profile lite up -d --build
  then sign in to the app as the admin.
`)
	return nil
}

type initAnswers struct {
	name, pack, colour, currency, symbol, minorUnits, timezone, centre, locale, adminEmail, origin string
}

func (a initAnswers) brandYAML() string {
	lat, lng, _ := strings.Cut(a.centre, ",")
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) } // a JSON string is a valid YAML scalar
	var b strings.Builder
	fmt.Fprintf(&b, `# Written by platformctl init on %s. Every field is explained in
# docs/CONFIGURATION.md; the server checks this file when it starts.
schema: 1
name: %s
colours: { primary: %s }
currency: { code: %s, symbol: %s, minor_units: %s }
locales: { default: %s, supported: [en, af] }
timezone: %s
map:
  centre: { lat: %s, lng: %s }
  zoom: 12
  # OpenStreetMap's own servers are for development only (docs/MAPS.md
  # says why and lists providers); the server refuses them outside dev.
  tiles:
    url: "https://tile.openstreetmap.org/{z}/{x}/{y}.png"
    attribution: "© OpenStreetMap contributors"
    max_zoom: 19
pack: %s
tenant_id: default
`, time.Now().UTC().Format("2006-01-02"), q(a.name), q(a.colour), a.currency, q(a.symbol), a.minorUnits, a.locale,
		q(a.timezone), strings.TrimSpace(lat), strings.TrimSpace(lng), a.pack)
	return b.String()
}

// envFile is .env.example with this install's values filled in, so every
// explanation in it stays next to its setting.
func (a initAnswers) envFile(prod bool, secret, password string) (string, error) {
	example, err := os.ReadFile(".env.example")
	if err != nil {
		return "", fmt.Errorf("%w: run init from the repository root", err)
	}
	profile := "dev"
	if prod {
		profile = "prod"
	}
	values := map[string]string{
		"PROFILE": profile, "JWT_SECRET": secret, "ALLOWED_ORIGINS": a.origin,
		"BOOTSTRAP_ADMIN_EMAIL": a.adminEmail, "BOOTSTRAP_ADMIN_PASSWORD": password,
	}
	lines := strings.Split(string(example), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if v, want := values[key]; ok && want {
			lines[i] = key + "=" + v
			delete(values, key)
		}
	}
	if len(values) > 0 {
		return "", fmt.Errorf(".env.example is missing %v: restore it from the release", keys(values))
	}
	return strings.Join(lines, "\n"), nil
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func checkBrand(text string) error {
	dir, err := os.MkdirTemp("", "platformctl-brand")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "brand.yaml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return err
	}
	// Loaded as dev: the OpenStreetMap tiles init writes are refused in
	// production on purpose, and init says so.
	_, err = brand.Load(p, true)
	return err
}

func writeKeepingOld(path, text string, force bool) error {
	if _, err := os.Stat(path); err == nil {
		if !force {
			return fmt.Errorf("%s exists", path)
		}
		if err := os.Rename(path, path+".bak"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600) // .env holds secrets
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// asker asks one question at a time, re-asking until the answer passes
// its check. Without a terminal (--yes), a flag or a default must answer
// it, and the first unanswered question is the error.
type asker struct {
	in  *bufio.Reader
	out io.Writer
	ask bool
	err error
}

func (q *asker) value(prompt, given, def string, check func(string) error) string {
	if q.err != nil {
		return ""
	}
	if given != "" {
		if err := check(given); err != nil {
			q.err = fmt.Errorf("%s: %w", prompt, err)
		}
		return given
	}
	if !q.ask {
		if def == "" {
			q.err = fmt.Errorf("%s has no default: pass its flag (platformctl init -h)", prompt)
		}
		return def
	}
	for {
		if def != "" {
			fmt.Fprintf(q.out, "%s [%s]: ", prompt, def)
		} else {
			fmt.Fprintf(q.out, "%s: ", prompt)
		}
		line, err := q.in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			line = def
		}
		if line != "" {
			if cerr := check(line); cerr == nil {
				return line
			} else {
				fmt.Fprintf(q.out, "  %v\n", cerr)
			}
		}
		if err != nil { // end of input: no more answers are coming
			q.err = fmt.Errorf("%s: no answer", prompt)
			return ""
		}
	}
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func matches(pattern, what string) func(string) error {
	re := regexp.MustCompile(pattern)
	return func(s string) error {
		if !re.MatchString(s) {
			return fmt.Errorf("%q: expected %s", s, what)
		}
		return nil
	}
}

func zone(s string) error {
	if _, err := time.LoadLocation(s); err != nil || s == "" || s == "Local" {
		return fmt.Errorf("%q is not an IANA zone like Africa/Johannesburg", s)
	}
	return nil
}

func latLng(s string) error {
	lat, lng, ok := strings.Cut(s, ",")
	la, err1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	ln, err2 := strconv.ParseFloat(strings.TrimSpace(lng), 64)
	if !ok || err1 != nil || err2 != nil || la < -90 || la > 90 || ln < -180 || ln > 180 {
		return fmt.Errorf("%q: expected lat,lng like -26.20,28.04", s)
	}
	return nil
}

func packExists(s string) error {
	if err := validName("pack", s); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join("packs", s, "pack.yaml")); err != nil {
		return fmt.Errorf("no pack %q in packs/ (platformctl new-pack makes one)", s)
	}
	return nil
}
