// Command platform runs every module in one process (ADR-0002): one
// Postgres database with a schema per module, the in-process events bus,
// and in-memory HTTP between modules. This is what a client's VPS runs
// (compose `pilot` profile).
//
// PLATFORM_MODULES limits which modules start (comma-separated names);
// by default all of them do. A disabled module's routes return 404.
package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/services/admin"
	"github.com/rainercoetzer/delivery-platform/services/customer"
	"github.com/rainercoetzer/delivery-platform/services/delivery"
	"github.com/rainercoetzer/delivery-platform/services/gateway"
	"github.com/rainercoetzer/delivery-platform/services/notification"
	"github.com/rainercoetzer/delivery-platform/services/order"
	"github.com/rainercoetzer/delivery-platform/services/payment"
	"github.com/rainercoetzer/delivery-platform/services/restaurant"
)

// all lists every module in start order. Order matters only for startup
// logs and migrations; modules talk through the bus and HTTP, never
// directly, so none depends on another being built first.
var all = []app.Definition{
	customer.Definition, restaurant.Definition, order.Definition, payment.Definition,
	delivery.Definition, notification.Definition, admin.Definition, gateway.Definition,
}

func main() {
	defs, err := selectModules(os.Getenv("PLATFORM_MODULES"), all)
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	app.Run(app.Options{Name: "platform", Definitions: defs, Embedded: true, DefaultPort: "8080"})
}

// selectModules returns the definitions named in list (comma-separated), or
// all of them for an empty list. An unknown name is an error, not a skip: a
// typo mustn't quietly boot a platform without its payments module.
func selectModules(list string, all []app.Definition) ([]app.Definition, error) {
	if strings.TrimSpace(list) == "" {
		return all, nil
	}
	byName := map[string]app.Definition{}
	names := make([]string, 0, len(all))
	for _, d := range all {
		byName[d.Name] = d
		names = append(names, d.Name)
	}
	var out []app.Definition
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		d, ok := byName[n]
		if !ok {
			return nil, &unknownModuleError{name: n, valid: names}
		}
		out = append(out, d)
	}
	return out, nil
}

type unknownModuleError struct {
	name  string
	valid []string
}

func (e *unknownModuleError) Error() string {
	return "PLATFORM_MODULES: unknown module " + `"` + e.name + `"` + " (valid: " + strings.Join(e.valid, ", ") + ")"
}
