// Package identity is the accounts module: users for every role, addresses,
// and the job-history projection. Definition runs it standalone
// (cmd/main.go) or inside cmd/platform.
package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/auth"
	"github.com/rainercoetzer/delivery-platform/pkg/config"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/api"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/consumers"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/migrations"
	"github.com/rainercoetzer/delivery-platform/services/identity/internal/store"
)

var Definition = app.Definition{
	Name:          "identity",
	FormerSchemas: []string{"customer"},
	Migrations:    migrations.FS,
	New:           newModule,
}

func newModule(d app.Deps) (*app.Module, error) {
	st := store.New(d.Pool)
	if err := bootstrapAdmin(context.Background(), st, d.Log); err != nil {
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	}
	a := &api.API{Store: st, Log: d.Log, Auth: d.Auth, Roles: d.Brand.EnabledRoles(d.Pack)}
	h := &consumers.Handlers{Pool: d.Pool, Log: d.Log}
	return &app.Module{
		Routes: a.Routes,
		Consumers: []app.Consumer{{
			Group:   "identity-service",
			Topics:  []string{events.JobCreated, events.JobStatusChanged},
			Handler: h.Handle,
		}},
	}, nil
}

// bootstrapAdmin creates the first admin from BOOTSTRAP_ADMIN_EMAIL and
// BOOTSTRAP_ADMIN_PASSWORD if that email isn't registered yet. It is the only
// way to get an admin: public registration creates customers only. Both
// unset means "don't bootstrap"; setting just one is a config mistake.
func bootstrapAdmin(ctx context.Context, st *store.Store, log *slog.Logger) error {
	email, password := os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return fmt.Errorf("set both BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD, or neither")
	}
	if !config.IsDev() && len(password) < 12 {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be at least 12 characters outside PROFILE=dev")
	}
	if _, err := st.GetByEmail(ctx, email); err == nil {
		log.Info("bootstrap admin already exists", "email", email)
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	u, err := api.NewUser(config.String("BOOTSTRAP_ADMIN_NAME", "Admin"), email, password, auth.RoleAdmin, "")
	if err != nil {
		return err
	}
	if err := st.CreateUser(ctx, u); err != nil && !errors.Is(err, store.ErrDuplicateEmail) {
		return err // duplicate = another replica won the race, which is fine
	}
	log.Info("bootstrap admin created", "email", email)
	return nil
}
