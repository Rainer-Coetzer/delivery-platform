package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/brand"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/identity"
)

// doctor runs the checks the server makes at boot, one at a time, so a
// buyer sees every problem at once instead of fixing them one restart at
// a time. Checks that depend on an earlier one are skipped, not failed,
// when it fails: one broken brand file is one problem, not five.
type doctor struct {
	out    io.Writer
	failed int
	warned int
}

func (d *doctor) ok(name, detail string) { fmt.Fprintf(d.out, "  ok    %-16s %s\n", name, detail) }
func (d *doctor) fail(name, detail string) {
	d.failed++
	fmt.Fprintf(d.out, "  FAIL  %-16s %s\n", name, detail)
}
func (d *doctor) warn(name, detail string) {
	d.warned++
	fmt.Fprintf(d.out, "  warn  %-16s %s\n", name, detail)
}
func (d *doctor) skip(name, why string) { fmt.Fprintf(d.out, "  -     %-16s skipped: %s\n", name, why) }

func runDoctor(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(out)
	envFile := fs.String("env", ".env", "the .env file compose will read")
	skipTools := fs.Bool("skip-tools", false, "check the configuration only, not docker, go and flutter")
	connect := fs.Bool("connect", false, "also connect to DATABASE_URL (from where you run this; compose's own URL points inside its network)")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	d := &doctor{out: out}

	if !*skipTools {
		fmt.Fprintln(out, "Tools")
		d.tool("docker", true, "runs the platform (docker compose)", "docker", "compose", "version")
		d.tool("go", false, "builds from source, new-module", "go", "version")
		d.tool("flutter", false, "builds the app (apps/delivery_app)", "flutter", "--version")
	}

	fmt.Fprintln(out, "Configuration")
	n, err := loadDotEnv(*envFile)
	switch {
	case err != nil:
		d.fail("env file", err.Error())
	case n == 0:
		d.warn("env file", *envFile+" not found or empty: checking the shell's environment only (copy .env.example, or run platformctl init)")
	default:
		d.ok("env file", fmt.Sprintf("%s: %d values", *envFile, n))
	}
	// Compose runs PROFILE=dev unless told otherwise (docker-compose.yml).
	if os.Getenv("PROFILE") == "" {
		_ = os.Setenv("PROFILE", "dev")
	}
	if config.IsDev() {
		d.warn("profile", "PROFILE=dev: insecure defaults are allowed. Set PROFILE=prod in .env before real users arrive, and run doctor again")
	} else {
		d.ok("profile", "PROFILE="+config.Profile()+": every production rule applies")
	}
	d.config(*connect)

	fmt.Fprintln(out)
	switch {
	case d.failed > 0:
		fmt.Fprintf(out, "%d problem(s) would stop the platform from starting. Fix them and run doctor again.\n", d.failed)
		return fmt.Errorf("%d check(s) failed", d.failed)
	case d.warned > 0:
		fmt.Fprintf(out, "Ready to start, with %d warning(s).\n", d.warned)
	default:
		fmt.Fprintln(out, "Ready to start.")
	}
	return nil
}

// tool checks a program is installed by running it. required ones fail the
// doctor; the others warn, since not every buyer builds everything.
func (d *doctor) tool(name string, required bool, whatFor string, argv ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	outp, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		msg := fmt.Sprintf("`%s` didn't run (%v): needed for: %s", strings.Join(argv, " "), err, whatFor)
		if required {
			d.fail(name, msg)
		} else {
			d.warn(name, msg)
		}
		return
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(outp)), "\n")
	d.ok(name, first)
}

func (d *doctor) config(connect bool) {
	if _, err := config.JWTSecret(); err != nil {
		d.fail("JWT secret", err.Error())
	} else {
		d.ok("JWT secret", "set")
	}
	if origins, err := config.AllowedOrigins(); err != nil {
		d.fail("allowed origins", err.Error())
	} else {
		d.ok("allowed origins", strings.Join(origins, ", "))
	}
	if err := identity.CheckBootstrapAdmin(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")); err != nil {
		d.fail("first admin", err.Error())
	} else if os.Getenv("BOOTSTRAP_ADMIN_EMAIL") == "" {
		d.warn("first admin", "BOOTSTRAP_ADMIN_EMAIL is unset: no admin is created, and only an admin can add merchants and agents (docker-compose.dev.yml sets one for local use)")
	} else {
		d.ok("first admin", os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	}

	br, err := brand.Load(app.BrandFile(), config.IsDev())
	if err != nil {
		d.fail("brand file", err.Error())
	} else {
		d.ok("brand file", fmt.Sprintf("%s: %s, pack %s, %s", app.BrandFile(), br.Name, br.Pack, br.Currency.Code))
	}
	pack, err := app.LoadPack()
	if err != nil {
		d.fail("pack", err.Error())
		d.skip("strategies", "the pack didn't load")
		d.skip("brand and pack", "the pack didn't load")
		d.skip("file storage", "the pack didn't load")
	} else {
		d.ok("pack", fmt.Sprintf("%s: modules %s", pack.Name, strings.Join(pack.Modules, ", ")))
		d.packChecks(br, pack)
	}

	switch dsn := os.Getenv("DATABASE_URL"); {
	case dsn == "":
		d.ok("database", "compose provides DATABASE_URL")
	case !connect:
		d.ok("database", "DATABASE_URL is set (add --connect to try it)")
	default:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			err = conn.Ping(ctx)
			_ = conn.Close(ctx)
		}
		if err != nil {
			d.fail("database", "can't connect with DATABASE_URL: "+err.Error())
		} else {
			d.ok("database", "connected")
		}
	}
}

func (d *doctor) packChecks(br *brand.Brand, pack *packs.Pack) {
	strategies, err := app.BuildStrategies(pack)
	if err != nil {
		d.fail("strategies", err.Error())
		d.skip("brand and pack", "the strategies didn't build")
	} else {
		d.ok("strategies", fmt.Sprintf("payment: %s", strings.Join(strategies.Payments.Methods(), ", ")))
		switch {
		case br == nil:
			d.skip("brand and pack", "the brand file didn't load")
		case pack.Name != br.Pack:
			d.warn("brand and pack", fmt.Sprintf("PACK=%s overrides the brand's pack %s; the brand's overrides won't apply (unset PACK to use the brand's)", pack.Name, br.Pack))
		default:
			if err := br.Apply(pack, strategies.Payments.Methods()); err != nil {
				d.fail("brand and pack", err.Error())
			} else if _, err := strategies.Payments.Only(br.EnabledPaymentMethods(pack, strategies.Payments.Methods())); err != nil {
				d.fail("brand and pack", err.Error())
			} else {
				d.ok("brand and pack", fmt.Sprintf("roles %s", strings.Join(br.EnabledRoles(pack), ", ")))
			}
		}
	}
	if dir, err := app.BlobDir(pack); err != nil {
		d.fail("file storage", err.Error())
	} else if dir == "" {
		d.ok("file storage", "the pack keeps no files")
	} else {
		d.ok("file storage", dir)
	}
}
