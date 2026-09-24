package db

import (
	"net/url"
	"strings"
	"testing"
)

// Regression: the search_path must survive a round trip through a DSN query
// string with public as its own entry (not "+public").
func TestWithSearchPath_NoPlusInPath(t *testing.T) {
	dsn, err := withSearchPath("postgres://u:p@h:5432/db?sslmode=disable", "delivery")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(dsn)
	got := u.Query().Get("search_path")
	if got != `"delivery",public` {
		t.Fatalf("search_path = %q", got)
	}
	if strings.Contains(u.RawQuery, "+") {
		t.Fatalf("raw query contains '+', which pgx would keep literally: %s", u.RawQuery)
	}
}

func TestValidSchema(t *testing.T) {
	for name, want := range map[string]bool{"orders": true, "bus": true, "my_mod2": true, "Order": false, "a-b": false, "": false, "1x": false} {
		if got := validSchema.MatchString(name); got != want {
			t.Errorf("validSchema(%q) = %v, want %v", name, got, want)
		}
	}
}
