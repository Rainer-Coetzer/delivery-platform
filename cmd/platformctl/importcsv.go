package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	client "github.com/rainercoetzer/delivery-platform/pkg/apiclient"
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// An importer knows one entity's CSV columns, checks a row without the
// server, and sends it. It goes through the gateway's API as an admin, the
// same way the seed tool does, so every server rule applies and no module's
// tables are written behind its back.
type importer struct {
	columns  []column
	needs    string // a pack module the entity needs, or ""
	existing func(s *session) (map[string]string, error)
	key      func(row map[string]string) string // what "already exists" is judged on
	send     func(s *session, row map[string]string) error
	check    func(row map[string]string, br *brand.Brand) error
}

type column struct {
	name     string
	required bool
	help     string
}

var importers = map[string]importer{
	"merchants": {
		needs: packs.ModCatalogue,
		columns: []column{
			{"name", true, "the merchant's name"},
			{"lat", true, "latitude, e.g. -22.5700"},
			{"lng", true, "longitude, e.g. 17.0836"},
			{"opens_at", true, "HH:MM, local time"},
			{"closes_at", true, "HH:MM; the same as opens_at means open all day"},
			{"timezone", false, "IANA zone; empty uses the brand's"},
		},
		check: func(row map[string]string, br *brand.Brand) error {
			lat, lng, err := point(row)
			if err != nil {
				return err
			}
			if !br.InServiceArea(lat, lng) {
				return errors.New("outside the service area in config/brand.yaml: no job could start here")
			}
			for _, c := range []string{"opens_at", "closes_at"} {
				if _, err := time.Parse("15:04", row[c]); err != nil {
					return fmt.Errorf("%s %q is not HH:MM", c, row[c])
				}
			}
			if tz := row["timezone"]; tz != "" {
				if _, err := time.LoadLocation(tz); err != nil {
					return fmt.Errorf("timezone %q is not an IANA zone (e.g. Africa/Windhoek)", tz)
				}
			}
			return nil
		},
		existing: func(s *session) (map[string]string, error) { return s.names("/merchants") },
		key:      func(row map[string]string) string { return row["name"] },
		send: func(s *session, row map[string]string) error {
			lat, lng, _ := point(row)
			return s.post("/merchants", map[string]any{"name": row["name"], "lat": lat, "lng": lng,
				"opensAt": row["opens_at"], "closesAt": row["closes_at"], "timezone": row["timezone"]})
		},
	},
	"catalogue": {
		needs: packs.ModCatalogue,
		columns: []column{
			{"merchant", true, "the merchant's exact name (import merchants first)"},
			{"name", true, "the item's name"},
			{"price", true, "in the brand's currency, e.g. 65.00"},
			{"stock", false, "how many are in stock; empty is 0"},
		},
		check: func(row map[string]string, br *brand.Brand) error {
			if _, err := brand.ParseMoney(row["price"], br.Currency); err != nil {
				return fmt.Errorf("price: %w", err)
			}
			if s := row["stock"]; s != "" {
				if n, err := strconv.Atoi(s); err != nil || n < 0 {
					return fmt.Errorf("stock %q is not a whole number of 0 or more", s)
				}
			}
			return nil
		},
		// Items are matched per merchant, so "exists" is judged when sending.
		existing: func(s *session) (map[string]string, error) { return s.names("/merchants") },
		key:      func(row map[string]string) string { return "" },
		send: func(s *session, row map[string]string) error {
			id, ok := s.existing[row["merchant"]]
			if !ok {
				return fmt.Errorf("no merchant named %q (import merchants first)", row["merchant"])
			}
			path := "/merchants/" + url.PathEscape(id) + "/catalogue-items"
			items, err := s.names(path)
			if err != nil {
				return err
			}
			if _, dup := items[row["name"]]; dup {
				return errExists
			}
			price, _ := brand.ParseMoney(row["price"], s.brand.Currency)
			stock, _ := strconv.Atoi(row["stock"])
			return s.post(path, map[string]any{"name": row["name"], "priceCents": price, "stockQty": stock, "available": true})
		},
	},
	"agents": {
		columns: []column{
			{"name", true, "the agent's name"},
			{"vehicle", false, "e.g. bike, car, van; empty is bike"},
			{"lat", true, "where they start"},
			{"lng", true, "where they start"},
			{"capacity_g", false, "how much they carry, in grams (capacity module)"},
			{"capacity_cm3", false, "how much fits, in cm³ (capacity module)"},
			{"capabilities", false, "semicolon-separated, e.g. signature_pad;refrigerated"},
		},
		check: func(row map[string]string, br *brand.Brand) error {
			if _, _, err := point(row); err != nil {
				return err
			}
			for _, c := range []string{"capacity_g", "capacity_cm3"} {
				if s := row[c]; s != "" {
					if n, err := strconv.ParseInt(s, 10, 64); err != nil || n < 0 {
						return fmt.Errorf("%s %q is not a whole number of 0 or more", c, s)
					}
				}
			}
			return nil
		},
		existing: func(s *session) (map[string]string, error) { return s.names("/agents") },
		key:      func(row map[string]string) string { return row["name"] },
		send: func(s *session, row map[string]string) error {
			lat, lng, _ := point(row)
			capG, _ := strconv.ParseInt(row["capacity_g"], 10, 64)
			capCm3, _ := strconv.ParseInt(row["capacity_cm3"], 10, 64)
			var caps []string
			for _, c := range strings.Split(row["capabilities"], ";") {
				if c = strings.TrimSpace(c); c != "" {
					caps = append(caps, c)
				}
			}
			return s.post("/agents", map[string]any{"name": row["name"], "vehicle": row["vehicle"], "lat": lat, "lng": lng,
				"capacityGrams": capG, "capacityCm3": capCm3, "capabilities": caps})
		},
	},
}

var errExists = errors.New("already exists")

// bom is the byte-order mark, U+FEFF, as an escape: the character itself
// is invisible in an editor and illegal in Go source.
const bom = string(rune(0xFEFF))

func point(row map[string]string) (float64, float64, error) {
	lat, err1 := strconv.ParseFloat(row["lat"], 64)
	lng, err2 := strconv.ParseFloat(row["lng"], 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, fmt.Errorf("lat %q / lng %q is not a point (latitude -90..90, longitude -180..180)", row["lat"], row["lng"])
	}
	return lat, lng, nil
}

func runImport(args []string, out io.Writer) error {
	pos, flags := splitArgs(args, map[string]bool{"gateway": true, "email": true, "env": true})
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(out)
	dryRun := fs.Bool("dry-run", false, "check every row and send nothing")
	gateway := fs.String("gateway", "", "the gateway's URL (default GATEWAY_URL, else http://localhost:8080)")
	email := fs.String("email", "", "an admin's email (default BOOTSTRAP_ADMIN_EMAIL); the password is read from PLATFORMCTL_PASSWORD, else BOOTSTRAP_ADMIN_PASSWORD")
	envFile := fs.String("env", ".env", "the .env file to read")
	if err := fs.Parse(flags); err != nil || len(pos) != 2 {
		if err == nil {
			importUsage(out)
		}
		return errUsage
	}
	imp, ok := importers[pos[0]]
	if !ok {
		importUsage(out)
		return errUsage
	}
	if _, err := loadDotEnv(*envFile); err != nil {
		return err
	}
	if os.Getenv("PROFILE") == "" {
		_ = os.Setenv("PROFILE", "dev") // compose's default, as in doctor
	}
	br, err := brand.Load(app.BrandFile(), config.IsDev())
	if err != nil {
		return fmt.Errorf("the brand decides currency and service area, and it doesn't load: %w", err)
	}
	if imp.needs != "" {
		pack, err := app.LoadPack()
		if err != nil {
			return err
		}
		if !pack.Has(imp.needs) {
			return fmt.Errorf("pack %s has no %s module, so it has no %s", pack.Name, imp.needs, pos[0])
		}
	}

	rows, header, err := readRows(pos[1], imp.columns)
	if err != nil {
		return err
	}
	var rejected []rejection
	var good []csvRow
	for _, r := range rows {
		if err := required(r.values, imp.columns); err != nil {
			rejected = append(rejected, rejection{r, err})
		} else if err := imp.check(r.values, br); err != nil {
			rejected = append(rejected, rejection{r, err})
		} else {
			good = append(good, r)
		}
	}

	if *dryRun {
		fmt.Fprintf(out, "%s: %d row(s); %d would be sent, %d rejected. Nothing was sent (--dry-run).\n", pos[1], len(rows), len(good), len(rejected))
		return report(out, pos[1], header, rejected)
	}

	s, err := login(*gateway, *email, br)
	if err != nil {
		return err
	}
	if s.existing, err = imp.existing(s); err != nil {
		return err
	}
	sent, skipped := 0, 0
	for _, r := range good {
		if k := imp.key(r.values); k != "" {
			if _, dup := s.existing[k]; dup {
				skipped++
				fmt.Fprintf(out, "  line %d: %q already exists, skipped\n", r.line, k)
				continue
			}
		}
		switch err := imp.send(s, r.values); {
		case errors.Is(err, errExists):
			skipped++
			fmt.Fprintf(out, "  line %d: already exists, skipped\n", r.line)
		case err != nil:
			rejected = append(rejected, rejection{r, err})
		default:
			sent++
		}
	}
	fmt.Fprintf(out, "%s: %d row(s); %d sent, %d skipped (already there), %d rejected.\n", pos[1], len(rows), sent, skipped, len(rejected))
	return report(out, pos[1], header, rejected)
}

func importUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: platformctl import <entity> <file.csv> [--dry-run]")
	fmt.Fprintln(out, "\nThe first row names the columns, in any order:")
	for _, name := range []string{"merchants", "catalogue", "agents"} {
		fmt.Fprintf(out, "\n  %s\n", name)
		for _, c := range importers[name].columns {
			req := ""
			if c.required {
				req = " (required)"
			}
			fmt.Fprintf(out, "    %-13s %s%s\n", c.name, c.help, req)
		}
	}
}

type csvRow struct {
	line   int
	raw    []string
	values map[string]string
}

type rejection struct {
	row csvRow
	err error
}

// readRows reads the file, strictly: a header naming columns the entity
// doesn't have is an error (a typo'd column would otherwise be silently
// empty), and a row with the wrong number of fields is rejected by csv.
func readRows(path string, columns []column) ([]csvRow, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: the first row must name the columns: %w", path, err)
	}
	known := map[string]bool{}
	for _, c := range columns {
		known[c.name] = true
	}
	for i, h := range header {
		header[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, bom))) // Excel starts its CSVs with one
		if !known[header[i]] {
			return nil, nil, fmt.Errorf("%s: unknown column %q (platformctl import -h lists them)", path, h)
		}
	}
	var rows []csvRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line, _ := r.FieldPos(0)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		values := map[string]string{}
		for i, v := range rec {
			values[header[i]] = strings.TrimSpace(v)
		}
		rows = append(rows, csvRow{line: line, raw: rec, values: values})
	}
	return rows, header, nil
}

func required(row map[string]string, columns []column) error {
	var missing []string
	for _, c := range columns {
		if c.required && row[c.name] == "" {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

// report lists the rejected rows and writes them, with the reason, to
// <file>.rejected.csv: fix that file and import it. Any rejection makes the
// command fail, so a script or CI notices.
func report(out io.Writer, path string, header []string, rejected []rejection) error {
	if len(rejected) == 0 {
		return nil
	}
	leftovers := strings.TrimSuffix(path, ".csv") + ".rejected.csv"
	f, err := os.Create(leftovers)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write(append(append([]string{}, header...), "error"))
	for _, r := range rejected {
		fmt.Fprintf(out, "  line %d: %v\n", r.row.line, r.err)
		_ = w.Write(append(append([]string{}, r.row.raw...), r.err.Error()))
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return fmt.Errorf("%d row(s) rejected; they are in %s with the reason", len(rejected), leftovers)
}

// session is a signed-in admin at the gateway.
type session struct {
	gateway  string
	token    string
	brand    *brand.Brand
	existing map[string]string // name -> id, of what the entity already has
}

func login(gateway, email string, br *brand.Brand) (*session, error) {
	if gateway == "" {
		gateway = config.String("GATEWAY_URL", "http://localhost:8080")
	}
	if email == "" {
		email = os.Getenv("BOOTSTRAP_ADMIN_EMAIL")
	}
	password := os.Getenv("PLATFORMCTL_PASSWORD")
	if password == "" {
		password = os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	}
	if email == "" || password == "" {
		return nil, errors.New("an admin must sign in: pass --email and set PLATFORMCTL_PASSWORD (or BOOTSTRAP_ADMIN_* in .env)")
	}
	var res struct{ Token string }
	if err := client.Do(http.MethodPost, gateway+"/auth/login", "", map[string]string{"email": email, "password": password}, &res); err != nil {
		return nil, fmt.Errorf("sign in as %s at %s: %w", email, gateway, err)
	}
	return &session{gateway: gateway, token: res.Token, brand: br}, nil
}

func (s *session) post(path string, body any) error {
	return client.Do(http.MethodPost, s.gateway+path, s.token, body, nil)
}

// names lists what's at path as name -> id.
func (s *session) names(path string) (map[string]string, error) {
	var list []struct{ ID, Name string }
	if err := client.Do(http.MethodGet, s.gateway+path, s.token, nil, &list); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, x := range list {
		out[x.Name] = x.ID
	}
	return out, nil
}
