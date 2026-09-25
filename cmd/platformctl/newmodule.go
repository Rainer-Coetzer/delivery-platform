package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

// The scaffold's files, as templates. Each is the smallest thing that
// shows the project's conventions (project conventions): a pure domain package with
// a table test, an append-only migration, a store, routes that declare
// their roles and take identity from the token, and a route-role test.
//
//go:embed all:scaffold
var scaffold embed.FS

const modulePath = "github.com/rainercoetzer/delivery-platform"

type moduleData struct {
	Name   string // returns
	Title  string // Returns
	Module string // the Go module path
}

func runNewModule(args []string, out io.Writer) error {
	pos, flags := splitArgs(args, map[string]bool{"root": true})
	fs := flag.NewFlagSet("new-module", flag.ContinueOnError)
	fs.SetOutput(out)
	withApp := fs.Bool("app", false, "also scaffold the app side (apps/delivery_app/lib/ext/<name>)")
	root := fs.String("root", ".", "the repository root")
	if err := fs.Parse(flags); err != nil || len(pos) != 1 {
		if err == nil {
			fmt.Fprintln(out, "usage: platformctl new-module <name> [--app]")
		}
		return errUsage
	}
	name := pos[0]
	if err := validName("module", name); err != nil {
		return err
	}
	// A module's name is its schema and its route prefix: it can't be one
	// we ship or one the buyer already has.
	for _, taken := range []string{filepath.Join(*root, "services", name), filepath.Join(*root, "ext", name)} {
		if _, err := os.Stat(taken); err == nil {
			return fmt.Errorf("%s exists: module %q is taken", taken, name)
		}
	}
	data := moduleData{Name: name, Title: strings.ToUpper(name[:1]) + name[1:], Module: modulePath}

	var created []string
	undo := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.RemoveAll(created[i])
		}
	}
	dir := filepath.Join(*root, "ext", name)
	if err := render("scaffold/go", dir, data); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	created = append(created, dir)
	if *withApp {
		appDir := filepath.Join(*root, "apps", "delivery_app", "lib", "ext", name)
		if err := render("scaffold/app", appDir, data); err != nil {
			undo()
			_ = os.RemoveAll(appDir)
			return err
		}
		created = append(created, appDir)
	}

	// Wire it in last, once every file exists: a half-made module is never
	// registered.
	goFile := filepath.Join(*root, "cmd", "platform", "extensions.go")
	if err := insertAtMarkers(goFile, map[string]string{
		"// platformctl:imports":     fmt.Sprintf("%q", modulePath+"/ext/"+name),
		"// platformctl:definitions": name + ".Definition,",
	}, true); err != nil {
		undo()
		return err
	}
	if *withApp {
		dartFile := filepath.Join(*root, "apps", "delivery_app", "lib", "extensions.dart")
		if err := insertAtMarkers(dartFile, map[string]string{
			"// platformctl:imports": fmt.Sprintf("import 'ext/%s/%s_module.dart';", name, name),
			"// platformctl:modules": data.Title + "Module(),",
		}, false); err != nil {
			undo()
			return fmt.Errorf("%w (cmd/platform/extensions.go was already updated: remove the %s lines there too)", err, name)
		}
	}

	fmt.Fprintf(out, "Created ext/%s and registered it in cmd/platform/extensions.go.\n", name)
	if *withApp {
		fmt.Fprintf(out, "Created apps/delivery_app/lib/ext/%s and registered it in lib/extensions.dart.\n", name)
	}
	fmt.Fprintf(out, `
Check it:  go build ./... && go test ./ext/%[1]s/...
Try it:    docker compose up -d --build, then as an admin
           GET /ext/%[1]s/notes and POST /ext/%[1]s/notes {"body": "hello"}
Read:      ext/%[1]s/README.md (what each file is for, and what to change first)
`, name)
	return nil
}

// render writes every template under root in scaffold to dir, with the
// module's name in file names (NAME) and contents. Go files are gofmt'ed,
// which also proves they parse.
func render(root, dir string, data moduleData) error {
	return fs.WalkDir(scaffold, root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
		rel = strings.ReplaceAll(rel, "NAME", data.Name)
		src, err := scaffold.ReadFile(p)
		if err != nil {
			return err
		}
		t, err := template.New(path.Base(p)).Delims("[[", "]]").Parse(string(src))
		if err != nil {
			return fmt.Errorf("scaffold %s: %w", p, err)
		}
		var b bytes.Buffer
		if err := t.Execute(&b, data); err != nil {
			return fmt.Errorf("scaffold %s: %w", p, err)
		}
		content := b.Bytes()
		if strings.HasSuffix(rel, ".go") {
			if content, err = format.Source(content); err != nil {
				return fmt.Errorf("scaffold %s doesn't parse: %w", p, err)
			}
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
}

// insertAtMarkers adds each line just above its marker comment, indented
// like the marker. A missing marker is an error that says how to fix it,
// never a guess at where the line might go.
func insertAtMarkers(file string, lines map[string]string, gofmt bool) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%w: the file that registers your modules is missing; restore it from the release", err)
	}
	s := string(b)
	for marker, line := range lines {
		i := strings.Index(s, marker)
		if i < 0 {
			return fmt.Errorf("%s has no %q line: add it back where new lines should go", file, marker)
		}
		lineStart := strings.LastIndex(s[:i], "\n") + 1
		indent := s[lineStart:i]
		if strings.Contains(s, indent+line+"\n") {
			return fmt.Errorf("%s already has %q", file, line)
		}
		s = s[:lineStart] + indent + line + "\n" + s[lineStart:]
	}
	out := []byte(s)
	if gofmt {
		if out, err = format.Source(out); err != nil {
			return fmt.Errorf("%s doesn't parse after the edit: %w", file, err)
		}
	}
	return os.WriteFile(file, out, 0o644)
}
