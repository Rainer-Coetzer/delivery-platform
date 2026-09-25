package config

import (
	"fmt"
	"os"
	"strings"
)

// DevJWTSecret is what PROFILE=dev falls back to when JWT_SECRET is unset.
// Anywhere else, seeing this value is a boot failure.
const DevJWTSecret = "dev-secret-change-me-dev-secret-change-me"

// minSecretBytes: HS256 keys shorter than the 256-bit hash output are
// brute-forceable offline from any captured token.
const minSecretBytes = 32

// Profile is PROFILE, defaulting to "prod" on purpose: forgetting to set it
// must land on the strict side. docker-compose.yml sets PROFILE=dev for
// local work.
func Profile() string { return String("PROFILE", "prod") }

func IsDev() bool { return Profile() == "dev" }

// DemoAutopilot reads DEMO_AUTOPILOT. It stands in for people: it marks
// jobs ready, completes stops and records simulated proof. Outside
// PROFILE=dev that would be fabricated delivery records, so it refuses to
// boot rather than run.
func DemoAutopilot() (bool, error) {
	on, err := Bool("DEMO_AUTOPILOT", false)
	if err != nil {
		return false, err
	}
	if on && !IsDev() {
		return false, fmt.Errorf("DEMO_AUTOPILOT=true is for PROFILE=dev only (it completes jobs and records simulated proof); PROFILE is %q", Profile())
	}
	return on, nil
}

// JWTSecret returns JWT_SECRET. In dev a missing secret falls back to
// DevJWTSecret; outside dev a missing, short or dev-default secret is an
// error the service must refuse to boot on.
func JWTSecret() (string, error) {
	s := os.Getenv("JWT_SECRET")
	if IsDev() {
		if s == "" {
			return DevJWTSecret, nil
		}
		return s, nil
	}
	switch {
	case s == "":
		return "", fmt.Errorf("JWT_SECRET is required when PROFILE=%s", Profile())
	case s == DevJWTSecret || strings.Contains(s, "change-me"):
		return "", fmt.Errorf("JWT_SECRET is still the dev placeholder (PROFILE=%s)", Profile())
	case len(s) < minSecretBytes:
		return "", fmt.Errorf("JWT_SECRET is %d bytes; at least %d are required (PROFILE=%s)", len(s), minSecretBytes, Profile())
	}
	return s, nil
}

// AllowedOrigins returns ALLOWED_ORIGINS (comma-separated), used for CORS
// and WebSocket origin checks at the gateway. Dev defaults to "*"; outside
// dev a wildcard or an empty list is an error.
func AllowedOrigins() ([]string, error) {
	raw := os.Getenv("ALLOWED_ORIGINS")
	if raw == "" && IsDev() {
		return []string{"*"}, nil
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if IsDev() {
		return out, nil
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ALLOWED_ORIGINS is required when PROFILE=%s (e.g. https://app.example.com)", Profile())
	}
	for _, o := range out {
		if strings.Contains(o, "*") {
			return nil, fmt.Errorf("ALLOWED_ORIGINS may not contain wildcards when PROFILE=%s (got %q)", Profile(), o)
		}
	}
	return out, nil
}
