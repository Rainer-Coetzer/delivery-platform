# Conventions

The rules every module, pack and extension in this repo follows. The
`platformctl new-module` scaffold is built to them, so code you add from it
starts out compliant.

## Checks

```bash
go build ./... && go vet ./... && go test ./... -race -count=1 && gofmt -l .
TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable go test -tags=integration ./...   # needs `docker compose up -d --wait postgres`
./scripts/e2e.sh                     # lite profile: build, up, seed, the pack's demo job to completed
PACK=courier ./scripts/e2e.sh        # another pack (food, courier, freight)
E2E_PROFILE=full ./scripts/e2e.sh    # the microservice shape (Kafka)
go test ./tools/termcheck            # no food words outside packs/food/ unless scripts/terms-allowlist.txt says why
go run ./cmd/platformctl doctor      # the server's boot checks, all at once
cd apps/delivery_app && flutter analyze && flutter test
```

## Layout

- One folder per module: `services/<name>/{module.go,cmd,internal/{api,consumers,domain,store,migrations}}`. `module.go` is the module's `app.Definition`; `cmd/main.go` is one `app.Run` call; `cmd/platform` lists every module (buyers' ones in `cmd/platform/extensions.go`). Shared code goes only in `pkg/`.
- `cmd/platformctl` is the buyer's CLI ([PLATFORMCTL.md](PLATFORMCTL.md)); its commands and flags are a stable surface.
- The Flutter app mirrors the modules: `lib/core` (shared) and `lib/modules/<name>` with an `AppModule`; modules never import each other (`test/modules_test.dart`). Every UI string is in `lib/l10n/*.arb`.
- Modules never import each other. They talk through `events.Bus` or HTTP via `Deps.HTTP`/`Deps.URL` (in-memory inside the platform), forwarding the caller's `Authorization` header.
- Routes are registered flat enough to share one router (no second module may mount the same prefix). `/healthz`, `/readyz` and `/metrics` belong to `pkg/app`. Internal-only routes are listed in the gateway's `internalOnly`.
- Schema names are plain lowercase identifiers (`pkg/db.validSchema`).
- `pkg/events/events.go` is the single source of truth for topics and payloads. Changing a payload is a breaking change: add fields, don't rename them.
- `domain/` packages are pure (no DB, HTTP or Kafka) and have table-driven tests.
- State changes that emit events use `pool.WithTx` + `outbox.Insert`. Never publish from a handler directly; the only exception is ephemeral telemetry like `delivery.location`.
- Every consumer handler starts with `db.InsertIfNew` in the same transaction as its side effects.
- Migrations are append-only goose files (`0000N_name.sql`). Never edit a shipped migration.

## Extending

- The core speaks jobs, stops, legs, merchants and agents, and reacts to lifecycle milestones, never to a pack's state names ([ADR-0003](adr/0003-generic-logistics-model.md)).
- Extend by config, then by implementing one interface, then by adding a module ([ADR-0004](adr/0004-extension-model.md)). Keep your files (`packs/<yours>`, `ext/`, `config/`, `cmd/platform/extensions.go`) apart from the core's so upgrades merge cleanly.
- Every pack passes the core e2e suite plus its own scenarios ([TESTING.md](TESTING.md)).

## Tailorability

- No hardcoded business constants. Prices, rates, speeds, timeouts, currency, timezone, coordinates, colours and copy come from typed config or the brand and pack files.
- Business rules sit behind an interface with a registry (pricing, payment, dispatch, ETA, acceptance rules, notification channels), selected by config.
- Money is `int64` minor units plus a currency code. No float arithmetic on money.
- Times are stored in UTC and evaluated in the entity's timezone.
- The Flutter app gets brand, currency, map and enabled roles from `GET /config`, and reads enums from the API rather than duplicating them.

## Security

- Every service verifies the JWT itself (`auth.Issuer.Middleware`, [ADR-0001](adr/0001-identity-propagation.md)). Identity never comes from `customerId` and similar fields in request bodies.
- Every route declares its roles (`auth.RequireRole`) and checks ownership (`claims.ActsAs`, `userId`). Each service has a route-role matrix test (`internal/api/*_test.go`); a new route needs a row there.
- Client ids that go into URLs for other services are `url.PathEscape`d.
- Insecure dev defaults are allowed only when `PROFILE=dev`. Anywhere else, boot fails.
