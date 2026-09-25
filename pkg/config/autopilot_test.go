package config

import (
	"strings"
	"testing"
)

// The autopilot completes jobs and records simulated proof: anywhere but
// dev that would be fabricated delivery records.
func TestDemoAutopilotIsDevOnly(t *testing.T) {
	t.Setenv("DEMO_AUTOPILOT", "true")
	t.Setenv("PROFILE", "prod")
	if _, err := DemoAutopilot(); err == nil || !strings.Contains(err.Error(), "PROFILE=dev only") {
		t.Fatalf("outside dev it must refuse: %v", err)
	}
	t.Setenv("PROFILE", "dev")
	if on, err := DemoAutopilot(); err != nil || !on {
		t.Fatalf("in dev it's allowed: %v %v", on, err)
	}
	t.Setenv("DEMO_AUTOPILOT", "")
	t.Setenv("PROFILE", "prod")
	if on, err := DemoAutopilot(); err != nil || on {
		t.Fatalf("off is fine anywhere: %v %v", on, err)
	}
}
