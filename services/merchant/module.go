// Package merchant is the merchants module: merchants, catalogues and stock,
// accept/reject on new jobs, and the prep's mark-ready step.
package merchant

import (
	"fmt"
	"time"
	// Embeds the IANA tz database. Opening hours are judged in each
	// merchant's timezone (domain.OpenAtTime), and the Alpine runtime image
	// ships no tz data. It lives here, in the module, not in a main package:
	// that way every binary that includes this module (the service or
	// cmd/platform) gets it, and it can't be lost when a main is rewritten.
	_ "time/tzdata"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/prep"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/store"
)

var Definition = app.Definition{
	Name:          "merchant",
	FormerSchemas: []string{"restaurant"},
	Migrations:    migrations.FS,
	New:           newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	// Merchants and their catalogues are the catalogue module. A pack
	// without it (courier, freight) gets no merchant routes, consumers or
	// autopilot: nothing to serve, and nothing to react to.
	if !d.Pack.Has(packs.ModCatalogue) {
		d.Log.Info("catalogue module off in this pack: merchant module idle", "pack", d.Pack.Name)
		return &app.Module{}, nil
	}
	st := store.New(d.Pool)
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	m := &app.Module{
		Routes:    (&api.API{Pool: d.Pool, Store: st, Log: d.Log, Auth: d.Auth, DefaultTimezone: d.Brand.Timezone}).Routes,
		Outbox:    true,
		Consumers: []app.Consumer{{Group: "merchant-service", Topics: consumers.Topics, Handler: h.Handle}},
	}
	autopilot, err := config.DemoAutopilot()
	if err != nil {
		return nil, err
	}
	if autopilot {
		prepTime, err := config.Duration("AUTOPILOT_PREP_TIME", 20*time.Second)
		if err != nil {
			return nil, fmt.Errorf("autopilot: %w", err)
		}
		d.Log.Info("demo autopilot on: jobs are marked ready automatically", "prepTime", prepTime)
		m.Workers = append(m.Workers, (&prep.Autopilot{Pool: d.Pool, Store: st, Log: d.Log, PrepTime: prepTime}).Run)
	}
	return m, nil
}
