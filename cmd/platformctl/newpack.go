package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// runNewPack copies a pack to packs/<name>/ and renames it. The copy is
// loaded with the server's own rules before the command says it worked:
// a buyer starts from a pack that boots, and changes it from there.
func runNewPack(args []string, out io.Writer) error {
	pos, flags := splitArgs(args, map[string]bool{"from": true, "packs": true})
	fs := flag.NewFlagSet("new-pack", flag.ContinueOnError)
	fs.SetOutput(out)
	from := fs.String("from", "courier", "the pack to start from")
	dir := fs.String("packs", "packs", "the packs directory")
	if err := fs.Parse(flags); err != nil || len(pos) != 1 {
		if err == nil {
			fmt.Fprintln(out, "usage: platformctl new-pack <name> [--from courier]")
		}
		return errUsage
	}
	name := pos[0]
	if err := validName("pack", name); err != nil {
		return err
	}
	src, dst := filepath.Join(*dir, *from), filepath.Join(*dir, name)
	if _, err := os.Stat(filepath.Join(src, "pack.yaml")); err != nil {
		return fmt.Errorf("no pack %q to start from (%v); the shipped ones are food, courier and freight", *from, err)
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists; pick another name, or remove it first", dst)
	}

	if err := copyPack(src, dst, *from, name); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	p, err := packs.Load(os.DirFS(*dir), name, app.PackRules())
	if err != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("the copy doesn't load, so it was removed: %w", err)
	}

	fmt.Fprintf(out, "Created %s from %s (modules: %v).\n\n", dst, *from, p.Modules)
	fmt.Fprintf(out, `Next:
  1. Make it yours: %[1]s/pack.yaml (terminology, settings, strategies) and
     %[1]s/lifecycle.yaml (states and transitions). docs/CONFIGURATION.md
     and the comments in each file say what every field does.
  2. Use it: set "pack: %[2]s" in config/brand.yaml.
  3. Rebuild, since packs are built into the binary: docker compose build
     (or point PACKS_DIR at %[3]s to skip the rebuild while you edit).
  4. Check it: platformctl doctor, then PACK=%[2]s ./scripts/e2e.sh (it
     uses %[1]s/seed.yaml).
`, dst, name, *dir)
	return nil
}

// copyPack copies every file of the pack and renames it in pack.yaml. The
// name line is the one place a pack names itself (packs.Load checks it
// matches the directory).
func copyPack(src, dst, from, name string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // packs are flat; nothing below them is read
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if e.Name() == "pack.yaml" {
			nameLine := regexp.MustCompile(`(?m)^name:\s*` + regexp.QuoteMeta(from) + `\s*$`)
			if !nameLine.Match(b) {
				return fmt.Errorf("%s/pack.yaml has no line \"name: %s\" to rename", src, from)
			}
			b = nameLine.ReplaceAll(b, []byte("name: "+name))
			b = append([]byte(fmt.Sprintf("# Started from the %s pack (platformctl new-pack).\n", from)), b...)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
