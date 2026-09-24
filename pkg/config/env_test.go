package config

import (
	"testing"
	"time"
)

func TestBool(t *testing.T) {
	cases := []struct {
		name, val string
		fallback  bool
		want      bool
		wantErr   bool
	}{
		{"unset uses fallback", "", true, true, false},
		{"true", "true", false, true, false},
		{"one", "1", false, true, false},
		{"false", "false", true, false, false},
		{"typo fails loudly", "ture", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TEST_BOOL", c.val)
			got, err := Bool("TEST_BOOL", c.fallback)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFloatAndDuration(t *testing.T) {
	t.Setenv("TEST_FLOAT", "250.5")
	if f, err := Float("TEST_FLOAT", 0); err != nil || f != 250.5 {
		t.Fatalf("Float = %v, %v", f, err)
	}
	t.Setenv("TEST_FLOAT", "lots")
	if _, err := Float("TEST_FLOAT", 0); err == nil {
		t.Fatal("expected an error for a non-numeric value")
	}

	t.Setenv("TEST_DUR", "")
	if d, err := Duration("TEST_DUR", 15*time.Second); err != nil || d != 15*time.Second {
		t.Fatalf("Duration fallback = %v, %v", d, err)
	}
	t.Setenv("TEST_DUR", "10")
	if _, err := Duration("TEST_DUR", 0); err == nil {
		t.Fatal("a bare number is ambiguous and must be rejected")
	}
}
