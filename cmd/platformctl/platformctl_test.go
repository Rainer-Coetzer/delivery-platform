package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// repo is the repository root, seen from this package's directory.
var repo, _ = filepath.Abs(filepath.Join("..", ".."))

// isolate gives the test a clean environment for the variables the
// commands read or set, restored afterwards. loadDotEnv only fills unset
// variables, so each is registered with t.Setenv and then unset.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PROFILE", "JWT_SECRET", "ALLOWED_ORIGINS", "BOOTSTRAP_ADMIN_EMAIL", "BOOTSTRAP_ADMIN_PASSWORD",
		"PACK", "PACKS_DIR", "BRAND_FILE", "BLOB_DIR", "DATABASE_URL", "GATEWAY_URL", "PLATFORMCTL_PASSWORD", "MAP_TILES_KEY"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCmd(args ...string) (int, string) {
	var out bytes.Buffer
	code := run(args, &out, &out)
	return code, out.String()
}

func TestUsage(t *testing.T) {
	if code, out := runCmd(); code != 0 || !strings.Contains(out, "new-module") {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runCmd("frobnicate"); code != 2 || !strings.Contains(out, `unknown command "frobnicate"`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{"acme": true, "acme2": true, "": false, "Acme": false, "2acme": false, "my-pack": false, "my_pack": false} {
		if err := validName("pack", name); (err == nil) != ok {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	pos, flags := splitArgs([]string{"acme", "--from", "freight", "--app", "--root=x"}, map[string]bool{"from": true})
	if strings.Join(pos, ",") != "acme" || strings.Join(flags, " ") != "--from freight --app --root=x" {
		t.Fatalf("%v %v", pos, flags)
	}
}

func TestLoadDotEnv(t *testing.T) {
	isolate(t)
	t.Setenv("JWT_SECRET", "from-the-shell")
	p := filepath.Join(t.TempDir(), ".env")
	_ = os.WriteFile(p, []byte("# a comment\n\nPROFILE=prod\nJWT_SECRET=from-the-file\nALLOWED_ORIGINS=\"https://a.example\"\nexport BOOTSTRAP_ADMIN_EMAIL='x@example.com'\n"), 0o644)
	n, err := loadDotEnv(p)
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if os.Getenv("JWT_SECRET") != "from-the-shell" || os.Getenv("ALLOWED_ORIGINS") != "https://a.example" || os.Getenv("BOOTSTRAP_ADMIN_EMAIL") != "x@example.com" {
		t.Fatal("the shell wins; quotes and export are understood")
	}
	if n, err := loadDotEnv(filepath.Join(t.TempDir(), "missing")); n != 0 || err != nil {
		t.Fatalf("a missing file is fine: %d %v", n, err)
	}
	_ = os.WriteFile(p, []byte("NOT A SETTING\n"), 0o644)
	if _, err := loadDotEnv(p); err == nil || !strings.Contains(err.Error(), ":1: not KEY=VALUE") {
		t.Fatalf("a bad line names its line: %v", err)
	}
}

func TestDoctor(t *testing.T) {
	isolate(t)
	t.Setenv("BRAND_FILE", filepath.Join(repo, "config", "brand.yaml"))
	missing := filepath.Join(t.TempDir(), "none.env")

	// Dev, as compose runs it: ready, with warnings.
	code, out := runCmd("doctor", "--skip-tools", "--env", missing)
	if code != 0 || !strings.Contains(out, "ok    brand file") || !strings.Contains(out, "ok    pack             food") || !strings.Contains(out, "Ready to start") {
		t.Fatalf("dev: %d\n%s", code, out)
	}
	// Production with nothing set: every problem at once, then a failure.
	t.Setenv("PROFILE", "prod")
	code, out = runCmd("doctor", "--skip-tools", "--env", missing)
	for _, want := range []string{"FAIL  JWT secret", "FAIL  allowed origins", "FAIL  brand file", "usage policy", "would stop the platform"} {
		if !strings.Contains(out, want) {
			t.Errorf("prod: no %q in\n%s", want, out)
		}
	}
	if code != 1 {
		t.Fatalf("prod must fail: %d", code)
	}
	// A pack that doesn't exist: the checks that need it are skipped.
	t.Setenv("PROFILE", "dev")
	t.Setenv("PACK", "nosuch")
	_, out = runCmd("doctor", "--skip-tools", "--env", missing)
	if !strings.Contains(out, "FAIL  pack") || !strings.Contains(out, "skipped: the pack didn't load") {
		t.Fatalf("missing pack:\n%s", out)
	}
}

func TestNewPack(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"pack.yaml", "lifecycle.yaml", "seed.yaml"} {
		copyFile(t, filepath.Join(repo, "packs", "courier", f), filepath.Join(dir, "courier", f))
	}
	code, out := runCmd("new-pack", "acme", "--from", "courier", "--packs", dir)
	if code != 0 || !strings.Contains(out, `set "pack: acme"`) {
		t.Fatalf("%d %s", code, out)
	}
	p, err := packs.Load(os.DirFS(dir), "acme", app.PackRules())
	if err != nil || p.Name != "acme" {
		t.Fatalf("the new pack loads under its own name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "acme", "seed.yaml")); err != nil {
		t.Fatal("the demo data comes too: the e2e needs it")
	}
	for _, c := range [][]string{
		{"new-pack", "acme", "--packs", dir},                      // exists
		{"new-pack", "Acme", "--packs", dir},                      // bad name
		{"new-pack", "other", "--from", "nosuch", "--packs", dir}, // no source
	} {
		if code, out := runCmd(c...); code != 1 {
			t.Errorf("%v: %d %s", c, code, out)
		}
	}
	if code, _ := runCmd("new-pack"); code != 2 {
		t.Error("no name is a usage error")
	}
}

func TestNewModule(t *testing.T) {
	root := t.TempDir()
	copyFile(t, filepath.Join(repo, "cmd", "platform", "extensions.go"), filepath.Join(root, "cmd", "platform", "extensions.go"))
	copyFile(t, filepath.Join(repo, "apps", "delivery_app", "lib", "extensions.dart"), filepath.Join(root, "apps", "delivery_app", "lib", "extensions.dart"))
	_ = os.MkdirAll(filepath.Join(root, "services", "jobs"), 0o755)

	code, out := runCmd("new-module", "returns", "--app", "--root", root)
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	for _, f := range []string{"module.go", "README.md", "internal/api/api.go", "internal/api/api_test.go", "internal/domain/note.go",
		"internal/domain/note_test.go", "internal/migrations/00001_init.sql", "internal/store/store.go"} {
		b, err := os.ReadFile(filepath.Join(root, "ext", "returns", f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if strings.Contains(string(b), "[[") {
			t.Errorf("%s: an unfilled template field", f)
		}
	}
	ext, _ := os.ReadFile(filepath.Join(root, "cmd", "platform", "extensions.go"))
	if !strings.Contains(string(ext), `"github.com/rainercoetzer/delivery-platform/ext/returns"`) || !strings.Contains(string(ext), "returns.Definition,") {
		t.Fatalf("not registered:\n%s", ext)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "extensions.go", ext, 0); err != nil {
		t.Fatalf("extensions.go must still parse: %v", err)
	}
	dart, _ := os.ReadFile(filepath.Join(root, "apps", "delivery_app", "lib", "extensions.dart"))
	if !strings.Contains(string(dart), "import 'ext/returns/returns_module.dart';") || !strings.Contains(string(dart), "ReturnsModule(),") {
		t.Fatalf("app side not registered:\n%s", dart)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "delivery_app", "lib", "ext", "returns", "returns_screen.dart")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"returns", "jobs"} { // ours, or already made
		if code, out := runCmd("new-module", name, "--root", root); code != 1 || !strings.Contains(out, "is taken") {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
}

func TestInsertAtMarkersNeedsTheMarker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.go")
	_ = os.WriteFile(p, []byte("package main\n"), 0o644)
	if err := insertAtMarkers(p, map[string]string{"// platformctl:imports": `"x"`}, true); err == nil || !strings.Contains(err.Error(), "add it back") {
		t.Fatal(err)
	}
}

func TestInit(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	copyFile(t, filepath.Join(repo, ".env.example"), filepath.Join(dir, ".env.example"))
	copyFile(t, filepath.Join(repo, "packs", "courier", "pack.yaml"), filepath.Join(dir, "packs", "courier", "pack.yaml"))
	t.Chdir(dir)
	args := []string{"init", "--yes", "--name", `Acme "Fast" Couriers`, "--pack", "courier", "--currency", "zar", "--symbol", "R",
		"--timezone", "Africa/Johannesburg", "--centre", "-26.20,28.04", "--admin-email", "ops@acme.example"}

	code, out := runCmd(args...)
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	b, err := brand.Load(filepath.Join("config", "brand.yaml"), true)
	if err != nil || b.Name != `Acme "Fast" Couriers` || b.Currency.Code != "ZAR" || b.Pack != "courier" || b.Map.Centre.Lat != -26.2 {
		t.Fatalf("the brand loads with the answers: %+v %v", b, err)
	}
	env, _ := os.ReadFile(".env")
	for _, want := range []string{"PROFILE=dev\n", "BOOTSTRAP_ADMIN_EMAIL=ops@acme.example\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf(".env lacks %q", want)
		}
	}
	for _, line := range strings.Split(string(env), "\n") {
		if v, ok := strings.CutPrefix(line, "JWT_SECRET="); ok && len(v) < 32 {
			t.Errorf("a real secret: %q", v)
		}
	}
	if !strings.Contains(out, "password ") || !strings.Contains(out, "platformctl doctor") {
		t.Errorf("says the password once and what's next:\n%s", out)
	}

	// Never overwrites without --force, and keeps the old file with it.
	if code, out := runCmd(args...); code != 1 || !strings.Contains(out, "--force") {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runCmd(append(args, "--force")...); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join("config", "brand.yaml.bak")); err != nil {
		t.Fatal("the old brand is kept")
	}
	// --yes with a question nobody answered.
	_ = os.Remove(".env")
	_ = os.Remove(filepath.Join("config", "brand.yaml"))
	if code, out := runCmd("init", "--yes", "--name", "X"); code != 1 || !strings.Contains(out, "has no default") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestInitAsks(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	copyFile(t, filepath.Join(repo, ".env.example"), filepath.Join(dir, ".env.example"))
	copyFile(t, filepath.Join(repo, "packs", "freight", "pack.yaml"), filepath.Join(dir, "packs", "freight", "pack.yaml"))
	t.Chdir(dir)
	// A wrong answer is asked again; Enter takes the default.
	answers := "Haulers\nfreight\n\nnad\nN$\n\nMars/Olympus\nAfrica/Windhoek\n-22.57,17.08\naf\nme@example.com\n"
	old := stdin
	stdin = strings.NewReader(answers)
	t.Cleanup(func() { stdin = old })
	code, out := runCmd("init")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if !strings.Contains(out, "not an IANA zone") {
		t.Error("the bad zone was explained")
	}
	b, err := brand.Load(filepath.Join("config", "brand.yaml"), true)
	if err != nil || b.Colours.Primary != "#1F6FEB" || b.Locales.Default != "af" || b.Timezone != "Africa/Windhoek" {
		t.Fatalf("%+v %v", b, err)
	}
}

// A brand and CSVs in a temp dir, for the import tests.
func importSetup(t *testing.T, csvText string) string {
	t.Helper()
	isolate(t)
	t.Setenv("BRAND_FILE", filepath.Join(repo, "config", "brand.yaml")) // food, NAD, Windhoek 30 km
	p := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(p, []byte(csvText), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportDryRun(t *testing.T) {
	p := importSetup(t, bom+"name,price,merchant,stock\n"+
		"Platter,65.00,Kapana,3\n"+
		"Chips,0.295,Kapana,\n"+ // too many decimals for NAD
		",12,Kapana,1\n"+ // no name
		"Soup,12,Kapana,-1\n") // negative stock
	code, out := runCmd("import", "catalogue", p, "--dry-run", "--env", "none")
	if code != 1 || !strings.Contains(out, "4 row(s); 1 would be sent, 3 rejected") || !strings.Contains(out, "line 3: price") || !strings.Contains(out, "line 4: missing name") {
		t.Fatalf("%d\n%s", code, out)
	}
	leftovers, err := os.ReadFile(strings.TrimSuffix(p, ".csv") + ".rejected.csv")
	if err != nil || !strings.Contains(string(leftovers), "name,price,merchant,stock,error") || strings.Count(string(leftovers), "\n") != 4 {
		t.Fatalf("the rejected rows, with the reason, to fix and re-import: %v\n%s", err, leftovers)
	}

	p = importSetup(t, "name,lat,lng,opens_at,closes_at\nFar Away,-26.2,28.04,08:00,20:00\nNear,-22.57,17.08,8am,20:00\nGood,-22.57,17.08,08:00,20:00\n")
	code, out = runCmd("import", "merchants", p, "--dry-run", "--env", "none")
	if code != 1 || !strings.Contains(out, "outside the service area") || !strings.Contains(out, `opens_at "8am" is not HH:MM`) {
		t.Fatalf("%d\n%s", code, out)
	}

	p = importSetup(t, "name,lat,lng,colour\n")
	if code, out := runCmd("import", "agents", p, "--dry-run", "--env", "none"); code != 1 || !strings.Contains(out, `unknown column "colour"`) {
		t.Fatalf("a typo'd column is an error, not an empty field: %d %s", code, out)
	}
	if code, _ := runCmd("import", "customers", p); code != 2 {
		t.Fatal("an unknown entity is a usage error")
	}
}

// A real import against a fake gateway: it signs in, skips what exists,
// and sends the rest as the API expects.
func TestImportSends(t *testing.T) {
	var posted []map[string]any
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
		case r.Header.Get("Authorization") != "Bearer t":
			w.WriteHeader(401)
		case r.Method == "GET" && r.URL.Path == "/agents":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "a1", "name": "Already Here"}})
		case r.Method == "POST" && r.URL.Path == "/agents":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			posted = append(posted, body)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"a2"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer gw.Close()
	p := importSetup(t, "name,vehicle,lat,lng,capacity_g,capabilities\nAlready Here,bike,-22.5,17.0,,\nNew Rider,van,-22.5,17.0,500000,signature_pad; refrigerated\n")
	t.Setenv("PLATFORMCTL_PASSWORD", "pw")
	code, out := runCmd("import", "agents", p, "--gateway", gw.URL, "--email", "admin@example.com", "--env", "none")
	if code != 0 || !strings.Contains(out, "1 sent, 1 skipped") {
		t.Fatalf("%d\n%s", code, out)
	}
	if len(posted) != 1 || posted[0]["name"] != "New Rider" || posted[0]["capacityGrams"] != float64(500000) {
		t.Fatalf("%v", posted)
	}
	if caps := posted[0]["capabilities"].([]any); len(caps) != 2 || caps[1] != "refrigerated" {
		t.Fatalf("capabilities: %v", caps)
	}
}

func TestImportWontSendThePasswordInTheClear(t *testing.T) {
	for gw, ok := range map[string]bool{"https://api.acme.example": true, "http://localhost:18080": true, "http://127.0.0.1:8080": true,
		"http://api.acme.example": false, "http://10.0.0.5:8080": false, "api.acme.example": false} {
		if err := safeToSendPassword(gw); (err == nil) != ok {
			t.Errorf("%s: %v", gw, err)
		}
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
}

func TestBrandApp(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("BRAND_FILE", filepath.Join(repo, "config", "brand.yaml"))
	t.Chdir(dir)
	_ = os.MkdirAll("config", 0o755)
	writePNG(t, "icon.png", 1024, 1024)
	writePNG(t, "wide.png", 1024, 512)
	appDir := filepath.Join(dir, "app")
	files := map[string]string{
		"web/manifest.json":                        `{"name": "delivery_app", "short_name": "delivery_app", "start_url": ".", "theme_color": "#0175C2", "background_color": "#0175C2"}`,
		"web/index.html":                           `<head><meta name="description" content="A new Flutter project."><meta name="apple-mobile-web-app-title" content="delivery_app"><title>delivery_app</title></head>`,
		"android/app/src/main/AndroidManifest.xml": `<application android:label="delivery_app" android:name="${applicationName}">`,
		"android/app/build.gradle.kts":             "defaultConfig {\n    applicationId = \"com.example.delivery_app\"\n}",
		"ios/Runner.xcodeproj/project.pbxproj":     "PRODUCT_BUNDLE_IDENTIFIER = com.example.deliveryApp;\nPRODUCT_BUNDLE_IDENTIFIER = com.example.deliveryApp.RunnerTests;\n",
	}
	for f, text := range files {
		p := filepath.Join(appDir, filepath.FromSlash(f))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(text), 0o644)
	}

	code, out := runCmd("brand-app", "--bundle-id", "com.acme.eats", "--icon", "icon.png", "--app", appDir, "--api-url", "https://api.acme.example", "--env", "none")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	read := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(appDir, filepath.FromSlash(f)))
		return string(b)
	}
	var manifest map[string]any
	_ = json.Unmarshal([]byte(read("web/manifest.json")), &manifest)
	if manifest["name"] != "Windhoek Eats" || manifest["theme_color"] != "#E4572E" || manifest["start_url"] != "." {
		t.Errorf("manifest: %v", manifest)
	}
	if !strings.Contains(read("web/index.html"), "<title>Windhoek Eats</title>") || !strings.Contains(read("web/index.html"), `content="Anything in Windhoek, brought to you."`) {
		t.Errorf("index.html:\n%s", read("web/index.html"))
	}
	if !strings.Contains(read("android/app/src/main/AndroidManifest.xml"), `android:label="Windhoek Eats" android:name="${applicationName}"`) {
		t.Errorf("android label (and ${applicationName} untouched):\n%s", read("android/app/src/main/AndroidManifest.xml"))
	}
	if !strings.Contains(read("android/app/build.gradle.kts"), `applicationId = "com.acme.eats"`) {
		t.Error("application id")
	}
	if pbx := read("ios/Runner.xcodeproj/project.pbxproj"); pbx != "PRODUCT_BUNDLE_IDENTIFIER = com.acme.eats;\nPRODUCT_BUNDLE_IDENTIFIER = com.acme.eats.RunnerTests;\n" {
		t.Errorf("the app's id, and its test target's, kept apart:\n%s", pbx)
	}
	if !strings.Contains(out, "ios/Runner/Info.plist") {
		t.Error("the missing iOS folder is reported")
	}
	defines, _ := os.ReadFile(filepath.Join("config", "app.json"))
	if !strings.Contains(string(defines), `"APP_ID": "com.acme.eats"`) || !strings.Contains(string(defines), "api.acme.example") {
		t.Errorf("app.json: %s", defines)
	}
	if icons, _ := os.ReadFile(filepath.Join("config", "app_icons.yaml")); !strings.Contains(string(icons), "icon.png") {
		t.Errorf("icons config: %s", icons)
	}

	for _, c := range [][]string{
		{"--bundle-id", "Acme", "--icon", "icon.png"},
		{"--bundle-id", "com.acme.eats", "--icon", "wide.png"},
		{"--bundle-id", "com.acme.eats"}, // the brand's logo is an SVG
	} {
		if code, out := runCmd(append(append([]string{"brand-app"}, c...), "--app", appDir, "--env", "none")...); code != 1 {
			t.Errorf("%v: %d %s", c, code, out)
		}
	}
}
