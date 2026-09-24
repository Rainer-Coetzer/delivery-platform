// Package config reads settings from the environment. For now it only has
// typed getters; Phase 3 of docs/AUDIT.md grows this into one validated
// struct per service.
//
// Every getter returns an error naming the variable rather than falling back
// quietly: a typo like DEMO_AUTOPILOT=ture must stop the service at boot,
// not run with the flag silently off.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func String(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func Bool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("env %s=%q is not a boolean (use true or false)", key, v)
	}
	return b, nil
}

func Float(key string, fallback float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("env %s=%q is not a number", key, v)
	}
	return f, nil
}

// Duration parses Go duration syntax: "10s", "2m", "1h30m".
func Duration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("env %s=%q is not a duration (e.g. 10s, 2m)", key, v)
	}
	return d, nil
}
