// Command platformctl is the buyer's tool for setting up, checking and
// extending a platform. Run it from the repository root.
//
//	platformctl init                    set up brand.yaml and .env (asks)
//	platformctl doctor                  check tools and configuration
//	platformctl new-pack <name>         start a pack from another (--from courier)
//	platformctl new-module <name>       scaffold a module in ext/ (--app for the app side)
//	platformctl import <entity> <file>  load merchants, catalogue items or agents from CSV
//	platformctl brand-app               name, bundle id, icon and splash for the app
//
// Its commands and flags are a stable surface (ADR-0004): scripts may rely
// on them. Every command says what it changed, and none overwrites a file
// it didn't create unless told to (--force).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type command struct {
	summary string
	run     func(args []string, out io.Writer) error
}

var commands = map[string]command{
	"init":       {"set up config/brand.yaml and .env", runInit},
	"doctor":     {"check tools and configuration", runDoctor},
	"new-pack":   {"start a pack from another: new-pack <name> --from courier", runNewPack},
	"new-module": {"scaffold a module in ext/: new-module <name> [--app]", runNewModule},
	"import":     {"load CSV rows: import <merchants|catalogue|agents> <file.csv> [--dry-run]", runImport},
	"brand-app":  {"set the app's name, bundle id, icon and splash from the brand", runBrandApp},
}

// errUsage means the arguments were wrong; the command has already said how.
var errUsage = errors.New("usage")

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(out)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(errOut, "platformctl: unknown command %q\n\n", args[0])
		usage(errOut)
		return 2
	}
	if err := cmd.run(args[1:], out); err != nil {
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(errOut, "platformctl %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "platformctl: set up, check and extend the platform. Run from the repository root.")
	fmt.Fprintln(w)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-11s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(w, "\nplatformctl <command> -h shows a command's flags.")
}

// validName is the rule for pack and module names: they become directory
// names, Go package names and (for modules) database schema names
// (pkg/db.validSchema), so plain lowercase only.
func validName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	for i, r := range name {
		if !(r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return fmt.Errorf("%s name %q: use lowercase letters and digits, starting with a letter (it becomes a directory, a Go package and a schema name)", kind, name)
		}
	}
	return nil
}

// splitArgs separates positional arguments from flags, so flags may come
// after them (platformctl new-pack acme --from courier), which Go's flag
// package alone doesn't allow.
func splitArgs(args []string, valued map[string]bool) (positional, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if valued[name] && !strings.Contains(a, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return positional, flags
}
