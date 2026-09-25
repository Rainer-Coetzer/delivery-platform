package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/png" // icons must be PNG: DecodeConfig needs the decoder registered
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
)

// runBrandApp puts the brand on the app's native shells: its name and
// bundle id in whichever platform folders exist (web, android, ios; they
// are made by `flutter create`, see apps/delivery_app/README.md), and
// config for the icon and splash tools. The theme, words and currency
// need none of this: the app reads them from GET /config at runtime.
func runBrandApp(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("brand-app", flag.ContinueOnError)
	fs.SetOutput(out)
	bundleID := fs.String("bundle-id", "", "the app's id in the stores, reverse-DNS: com.acme.deliveries (required)")
	icon := fs.String("icon", "", "a square PNG, 1024 px or more (default: the brand's logo, if it's a PNG)")
	apiURL := fs.String("api-url", "", "the gateway address the app calls, e.g. https://api.example.com")
	appDir := fs.String("app", filepath.Join("apps", "delivery_app"), "the Flutter app")
	run := fs.Bool("run", false, "also run the icon and splash tools (needs flutter on PATH)")
	envFile := fs.String("env", ".env", "the .env file to read")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){2,}$`).MatchString(*bundleID) {
		return fmt.Errorf("--bundle-id %q: use reverse-DNS in lowercase, e.g. com.acme.deliveries (the stores never let you change it later)", *bundleID)
	}
	if _, err := loadDotEnv(*envFile); err != nil {
		return err
	}
	if os.Getenv("PROFILE") == "" {
		_ = os.Setenv("PROFILE", "dev")
	}
	br, err := brand.Load(app.BrandFile(), config.IsDev())
	if err != nil {
		return err
	}

	iconPath, err := pickIcon(*icon, br)
	if err != nil {
		return err
	}
	background := br.Colours.Background
	if background == "" {
		background = "#FFFFFF"
	}

	var changed, missing []string
	for _, p := range []struct {
		platform, file string
		edit           func(string) (string, error)
	}{
		{"web", "web/manifest.json", webManifest(br, background)},
		{"web", "web/index.html", replaceAll(
			[2]string{`<title>[^<]*</title>`, "<title>" + htmlEscape(br.Name) + "</title>"},
			[2]string{`(<meta name="apple-mobile-web-app-title" content=")[^"]*(")`, "${1}" + htmlEscape(br.Name) + "${2}"},
			[2]string{`(<meta name="description" content=")[^"]*(")`, "${1}" + htmlEscape(br.Tagline) + "${2}"},
		)},
		{"android", "android/app/src/main/AndroidManifest.xml", replaceAll([2]string{`(android:label=")[^"]*(")`, "${1}" + htmlEscape(br.Name) + "${2}"})},
		{"android", "android/app/build.gradle.kts", replaceAll([2]string{`(applicationId\s*=\s*")[^"]*(")`, "${1}" + *bundleID + "${2}"})},
		{"android", "android/app/build.gradle", replaceAll([2]string{`(applicationId\s+")[^"]*(")`, "${1}" + *bundleID + "${2}"})},
		{"ios", "ios/Runner/Info.plist", replaceAll(
			[2]string{`(<key>CFBundleDisplayName</key>\s*<string>)[^<]*(</string>)`, "${1}" + htmlEscape(br.Name) + "${2}"},
			[2]string{`(<key>CFBundleName</key>\s*<string>)[^<]*(</string>)`, "${1}" + htmlEscape(br.Name) + "${2}"},
		)},
		// The app's own id, and its test target's (<id>.RunnerTests).
		{"ios", "ios/Runner.xcodeproj/project.pbxproj", bundleIDs(*bundleID)},
	} {
		path := filepath.Join(*appDir, filepath.FromSlash(p.file))
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, p.file)
			continue
		}
		if err != nil {
			return err
		}
		next, err := p.edit(string(b))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if next != string(b) {
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				return err
			}
			changed = append(changed, p.file)
		}
	}

	// Build-time values, for flutter's --dart-define-from-file. In config/:
	// the buyer's, like the brand.
	defines := map[string]string{"APP_ID": *bundleID}
	if *apiURL != "" {
		defines["API_BASE_URL"] = *apiURL
	}
	if err := writeJSON(filepath.Join("config", "app.json"), defines); err != nil {
		return err
	}
	iconsCfg, splashCfg, err := writeToolConfigs(iconPath, br.Colours.Primary, background)
	if err != nil {
		return err
	}

	for _, f := range changed {
		fmt.Fprintf(out, "updated %s\n", filepath.Join(*appDir, f))
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "not there (yet): %s\n  Make the platform folders with `flutter create --platforms=android,ios,web .` in %s, then run brand-app again.\n", strings.Join(missing, ", "), *appDir)
	}
	fmt.Fprintf(out, "wrote config/app.json, %s and %s\n", iconsCfg, splashCfg)

	absIcons, _ := filepath.Abs(iconsCfg)
	absSplash, _ := filepath.Abs(splashCfg)
	steps := [][]string{
		{"dart", "run", "flutter_launcher_icons", "-f", absIcons},
		{"dart", "run", "flutter_native_splash:create", "--path=" + absSplash},
	}
	if *run {
		for _, s := range steps {
			fmt.Fprintf(out, "running %s\n", strings.Join(s, " "))
			cmd := exec.Command(s[0], s[1:]...)
			cmd.Dir, cmd.Stdout, cmd.Stderr = *appDir, out, out
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%s: %w", strings.Join(s, " "), err)
			}
		}
	} else {
		fmt.Fprintf(out, "\nIcons and splash (or pass --run), in %s:\n", *appDir)
		for _, s := range steps {
			fmt.Fprintf(out, "  %s\n", strings.Join(s, " "))
		}
	}
	fmt.Fprintf(out, "\nBuild with the brand's values:\n  flutter build appbundle --dart-define-from-file=%s\n",
		filepath.ToSlash(filepath.Join("..", "..", "config", "app.json")))
	return nil
}

// pickIcon checks the icon is a square PNG big enough for the stores
// (Apple asks for 1024 px). The brand's logo is used when it is one.
func pickIcon(given string, br *brand.Brand) (string, error) {
	if given == "" {
		logo, contentType := br.LogoFile()
		if contentType != "image/png" {
			return "", errors.New("the app icon must be a PNG: pass --icon icon.png (the brand's logo is not a PNG; SVG and JPEG don't make store icons)")
		}
		given = logo
	}
	f, err := os.Open(given)
	if err != nil {
		return "", fmt.Errorf("--icon: %w", err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "png" {
		return "", fmt.Errorf("--icon %s is not a PNG", given)
	}
	if cfg.Width != cfg.Height || cfg.Width < 512 {
		return "", fmt.Errorf("--icon %s is %dx%d; it must be square and at least 512 px (1024 is what the stores ask for)", given, cfg.Width, cfg.Height)
	}
	return given, nil
}

func writeToolConfigs(icon, primary, background string) (string, string, error) {
	absIcon, err := filepath.Abs(icon)
	if err != nil {
		return "", "", err
	}
	icons := filepath.Join("config", "app_icons.yaml")
	splash := filepath.Join("config", "app_splash.yaml")
	q := func(s string) string { b, _ := json.Marshal(filepath.ToSlash(s)); return string(b) }
	iconsText := fmt.Sprintf(`# Written by platformctl brand-app; run it again rather than editing.
# Read by flutter_launcher_icons.
flutter_launcher_icons:
  image_path: %s
  android: true
  adaptive_icon_background: %q
  adaptive_icon_foreground: %s
  ios: true
  remove_alpha_ios: true
  web:
    generate: true
    background_color: %q
    theme_color: %q
`, q(absIcon), background, q(absIcon), background, primary)
	splashText := fmt.Sprintf(`# Written by platformctl brand-app; run it again rather than editing.
# Read by flutter_native_splash.
flutter_native_splash:
  color: %q
  image: %s
  android_12:
    color: %q
    image: %s
`, background, q(absIcon), background, q(absIcon))
	for _, f := range [][2]string{{icons, iconsText}, {splash, splashText}} {
		if err := os.WriteFile(f[0], []byte(f[1]), 0o644); err != nil {
			return "", "", err
		}
	}
	return icons, splash, nil
}

// webManifest sets the web app's name and colours, keeping every other key.
func webManifest(br *brand.Brand, background string) func(string) (string, error) {
	return func(s string) (string, error) {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return "", err
		}
		m["name"], m["short_name"] = br.Name, br.Name
		m["theme_color"], m["background_color"] = br.Colours.Primary, background
		if br.Tagline != "" {
			m["description"] = br.Tagline
		}
		b, err := json.MarshalIndent(m, "", "    ")
		return string(b) + "\n", err
	}
}

// bundleIDs sets every PRODUCT_BUNDLE_IDENTIFIER in the Xcode project in
// one pass: the app's to id, the test target's to id.RunnerTests. (Two
// passes would let the second rewrite what the first just set.)
func bundleIDs(id string) func(string) (string, error) {
	re := regexp.MustCompile(`PRODUCT_BUNDLE_IDENTIFIER = ([^;]+);`)
	return func(s string) (string, error) {
		if !re.MatchString(s) {
			return "", errors.New("found no PRODUCT_BUNDLE_IDENTIFIER to change; set it in Xcode by hand")
		}
		return re.ReplaceAllStringFunc(s, func(m string) string {
			if strings.HasSuffix(re.FindStringSubmatch(m)[1], ".RunnerTests") {
				return "PRODUCT_BUNDLE_IDENTIFIER = " + id + ".RunnerTests;"
			}
			return "PRODUCT_BUNDLE_IDENTIFIER = " + id + ";"
		}), nil
	}
}

// replaceAll applies each (pattern, replacement) pair. A pattern that
// matches nothing is an error: a file brand-app can't read the way it
// expects is reported, not silently left unbranded.
func replaceAll(pairs ...[2]string) func(string) (string, error) {
	return func(s string) (string, error) {
		for _, p := range pairs {
			re := regexp.MustCompile(p[0])
			if !re.MatchString(s) {
				return "", fmt.Errorf("found no %s to change; has the file been reshaped? Set it by hand", p[0])
			}
			s = re.ReplaceAllString(s, p[1])
		}
		return s, nil
	}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "$", "$$").Replace(s)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
