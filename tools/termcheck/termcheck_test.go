// Package termcheck holds the terminology check: the core must not speak
// food. Every user-facing noun comes from a pack (packs/<name>/pack.yaml
// terminology), so outside packs/food/ the words restaurant, menu, food,
// driver, kitchen and dish may appear only where scripts/terms-allowlist.txt
// says why. It's a test so CI runs it with everything else:
//
//	go test ./tools/termcheck
package termcheck

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var foodWords = regexp.MustCompile(`(?i)restaurant|menu|food|driver|kitchen|dish`)

// What gets checked: source and config, not generated or vendored trees.
var checked = map[string]bool{".go": true, ".dart": true, ".sh": true, ".sql": true, ".yml": true, ".yaml": true, ".json": true}

var skipDirs = map[string]bool{".git": true, "build": true, ".dart_tool": true, "node_modules": true, "web": true, "docs": true,
	"android": true, "ios": true, "linux": true, "macos": true, "windows": true}

type rule struct{ glob, needle, why string }

func TestNoFoodWordsOutsideTheFoodPack(t *testing.T) {
	root := "../.."
	rules := loadRules(t, filepath.Join(root, "scripts", "terms-allowlist.txt"))
	var unexplained []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] || rel == "packs/food" {
				return filepath.SkipDir
			}
			return nil
		}
		if !checked[path.Ext(rel)] && d.Name() != "Dockerfile" {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if foodWords.MatchString(line) && !explained(rules, rel, line) {
				unexplained = append(unexplained, rel+":"+strconv.Itoa(n)+": "+strings.TrimSpace(line))
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unexplained) > 0 {
		t.Fatalf("food words outside packs/food/: take the word from the pack's terminology, "+
			"or add a line to scripts/terms-allowlist.txt saying why it belongs:\n  %s", strings.Join(unexplained, "\n  "))
	}
}

// Every allowlist rule must still match something: a rule for a line that
// has gone would quietly excuse the next one.
func TestAllowlistHasNoDeadRules(t *testing.T) {
	root := "../.."
	rules := loadRules(t, filepath.Join(root, "scripts", "terms-allowlist.txt"))
	used := make([]bool, len(rules))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] || rel == "packs/food" {
				return filepath.SkipDir
			}
			return nil
		}
		if !checked[path.Ext(rel)] && d.Name() != "Dockerfile" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !foodWords.MatchString(line) {
				continue
			}
			for i, r := range rules {
				if r.matches(rel, line) {
					used[i] = true
				}
			}
		}
		return nil
	})
	for i, r := range rules {
		if !used[i] {
			t.Errorf("allowlist rule %q|%q (%s) matches nothing any more: remove it", r.glob, r.needle, r.why)
		}
	}
}

func loadRules(t *testing.T, file string) []rule {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []rule
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			t.Fatalf("%s:%d: want <glob>|<text>|<why>, with a why", file, n+1)
		}
		out = append(out, rule{glob: parts[0], needle: strings.ToLower(parts[1]), why: parts[2]})
	}
	return out
}

func explained(rules []rule, rel, line string) bool {
	for _, r := range rules {
		if r.matches(rel, line) {
			return true
		}
	}
	return false
}

// matches: the glob against the whole path, or "*" for any file; the
// needle, case-insensitively, anywhere on the line.
func (r rule) matches(rel, line string) bool {
	ok := r.glob == "*"
	if !ok {
		ok, _ = path.Match(r.glob, rel)
	}
	if !ok {
		// "*_test.go" style globs match on the file name alone.
		ok, _ = path.Match(r.glob, path.Base(rel))
		ok = ok && !strings.Contains(r.glob, "/")
	}
	return ok && strings.Contains(strings.ToLower(line), r.needle)
}
