// Package strategytest turns a YAML snippet into strategy.Params, for the
// strategy kinds' tests (and a buyer's tests of their own strategies).
package strategytest

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Params parses src (a YAML mapping, or "" for none) as a strategy's params.
func Params(t testing.TB, where, src string) strategy.Params {
	t.Helper()
	if src == "" {
		return strategy.NewParams(where, nil)
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("params %q: %v", src, err)
	}
	return strategy.NewParams(where, n.Content[0])
}
