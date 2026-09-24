package config

import (
	"strings"
	"testing"
)

func TestJWTSecret(t *testing.T) {
	strong := strings.Repeat("k", 40)
	cases := []struct {
		name, profile, secret string
		wantErr               bool
	}{
		{"dev, unset -> dev default", "dev", "", false},
		{"dev, short is tolerated", "dev", "short", false},
		{"prod, unset", "prod", "", true},
		{"unset profile is prod", "", "", true},
		{"prod, dev placeholder", "prod", DevJWTSecret, true},
		{"prod, too short", "prod", "only-sixteen-byt", true},
		{"prod, strong", "prod", strong, false},
		{"staging behaves like prod", "staging", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PROFILE", c.profile)
			t.Setenv("JWT_SECRET", c.secret)
			_, err := JWTSecret()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestAllowedOrigins(t *testing.T) {
	cases := []struct {
		name, profile, raw string
		want               []string
		wantErr            bool
	}{
		{"dev default wildcard", "dev", "", []string{"*"}, false},
		{"prod requires a list", "prod", "", nil, true},
		{"prod rejects wildcard", "prod", "*", nil, true},
		{"prod rejects partial wildcard", "prod", "https://*.example.com", nil, true},
		{"prod list, trimmed", "prod", " https://a.example.com , https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PROFILE", c.profile)
			t.Setenv("ALLOWED_ORIGINS", c.raw)
			got, err := AllowedOrigins()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
