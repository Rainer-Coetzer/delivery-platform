# Testing

What proves each phase is done, and the **release gate**: the bar every
version of the product must clear before it's sold.

The design is in [ADR-0002](adr/0002-modular-monolith.md) and
[ADR-0003](adr/0003-generic-logistics-model.md). The first two hardening
phases are recorded in [AUDIT.md](AUDIT.md). A phase counts as done only
when its tests exist and pass, and the run has been reported honestly. If a
layer couldn't run, the phase summary says so.

## Layers

| Layer | Tool | Where | Runs |
|---|---|---|---|
| Domain (pure logic) | `go test`, table-driven | `*/domain`, `pkg/*` | every commit, CI |
| HTTP handlers + route-role matrix | `httptest`, fake auth | `*/internal/api/*_test.go` | every commit, CI |
| Integration (DB, bus) | `go test -tags=integration`, `TEST_DATABASE_URL` | `*_integration_test.go` | CI job with PostGIS; locally with compose Postgres |
| Bus contract | same suite against `inprocess` and `kafka` | `pkg/events/bus/bustest` | CI (`inprocess`); `kafka` in the `full`-profile job |
| Pack validation | `go test` over every `packs/*/` | `pkg/lifecycle`, `pkg/packs` | every commit |
| Stack e2e | `tools/e2e` via `scripts/e2e.sh` | repo root | per phase; every pack; CI compose job |
| Flutter | `flutter analyze && flutter test` | `apps/delivery_app/test/` | every commit, CI |

**Rules**

- Domain tests are table-driven (see [CONVENTIONS.md](CONVENTIONS.md)). Every strategy and every
  lifecycle rule ships with a table, including the invalid cases.
- Every route has a row in its service's route-role matrix. Every rejection
  must be decided before any store call (the tests run with nil stores, so a
  miss panics instead of passing).
- Integration tests skip only when `TEST_DATABASE_URL` is unset, and CI
  always sets it.
- `tools/e2e` exits non-zero with the last state and the reason. It never
  prints "failed" and exits 0.

## The pack rule

**Every vertical pack passes the same core e2e suite plus its own
scenarios.** Packs can't opt out of the core suite; they can only add to it.

`tools/e2e` runs in two parts:

1. **Core suite (pack-agnostic).** It reads the lifecycle, terminology and
   enabled modules from `GET /config`, never from hardcoded names.
   - **Security smoke:** public signup can't pick a role; a customer can't
     reach admin, merchant or agent routes; no token gives 401; the
     WebSocket needs a token and ownership.
   - **Happy path:** create a job through the pack's own job-creation flow
     (a catalogue pick or a package form, as the pack declares). Drive it
     with the autopilot through every state on one path to the `completed`
     milestone. Assert each state is visited in lifecycle order and the price
     came from the server.
   - **Cancellation:** cancel from the first cancellable state and assert
     `cancelled` plus compensation (refund if paid, agent freed, stock
     released if `catalogue` is on).
   - **Idempotency:** replay the job's events through the bus and assert no
     duplicate effects.
   - **Tracking:** WebSocket events arrive for the job's owner and are
     refused for another customer.
2. **Pack scenarios**, in `packs/<name>/e2e.yaml`: declarative steps
   (create, act as role, expect state or error) run by the same tool.
   - **food:** closed-hours rejection in the merchant's timezone, out of
     stock, a stale price.
   - **courier:** per-km and per-kg quote, photo proof required before the
     dropoff completes.
   - **freight (stub):** a job over vehicle capacity is not auto-assigned;
     manual assign; a quote-on-request flow.

A new pack is mergeable only when `./scripts/e2e.sh --pack <name>` passes
both parts.

## Phase gates

### Phase A: decide the shape

Documents only: ADR-0002, ADR-0003 and this file. **Gate:** the ADRs name
their options, the trade-offs and where they differ from the brief; the
existing check line and e2e still pass.

### Phase B: modular monolith

Status: **done** (24 September 2026). Contract suite green on both buses,
lite and full e2e, idle RAM ~126 MiB (ADR-0002 "Measured").

| Test | Type | Proves |
|---|---|---|
| Bus contract suite passes on `inprocess` **and** `kafka`: per-key ordering; at-least-once after a dispatcher crash mid-batch; dedupe via `processed_events`; permanent error → DLQ on the first attempt; transient error → retried; two groups each see every event | contract | the two implementations are interchangeable |
| Partitioning: events for one key always land in one partition; a stuck key blocks only its partition | integration | head-of-line blocking stays local |
| Migrations: every module migrates into its own schema; two platform replicas booting at once → one migrates, one waits | integration | schema per module + advisory lock |
| In-memory RoundTripper: an inter-module call runs the callee's authz as the end user | handler | ADR-0001 still holds in-process |
| `cmd/platform` with only some modules enabled boots, and routes of disabled modules return 404 | integration | config picks modules |
| Every per-service `cmd/` still compiles | CI build | microservice shape stays available |
| e2e passes on the `lite` profile (platform + Postgres + Caddy) | e2e | the monolith works end to end |
| Idle RSS of the `lite` profile measured (`docker stats`, 5 min after boot, seeded) and recorded in ADR-0002 | measurement | < 1 GB target |

### Phase C: generalise the domain

| Test | Type | Proves |
|---|---|---|
| Lifecycle validator table: unreachable state, a terminal state with exits, a dead end, zero or two triggers, unknown event/role/fact, missing required milestone, duplicate `completed` → each a named boot error | domain | packs are validated at boot |
| `packs/food/lifecycle.yaml` reproduces today's transition table exactly (generated comparison against the old `validTransitions`) | domain | no behaviour change for food |
| Rename migrations run on a database seeded with the pre-rename schema; row counts and a sample job's full history match | integration | data is preserved |
| Strategy registries: unknown name → boot error listing the valid names; each strategy's table | domain | pluggable per pack |
| Terminology grep: no `restaurant\|menu\|food\|driver\|kitchen\|dish` outside `packs/food/` except the allow-list | CI check | no food in the core |
| e2e core suite + scenarios pass for `food` and `courier`; `freight` passes the core suite and its stub scenarios | e2e | the model holds across verticals |

### Phase D: white-label and buyer configuration

| Test | Type | Proves |
|---|---|---|
| Brand + pack merge table: brand overrides pack defaults; unknown keys and invalid values fail at boot; `schema:` version checked | domain | config-only rebrand |
| `GET /config` contains only the public subset (a snapshot test fails if a secret-like key appears) | handler | no leakage |
| A job outside the service area (radius and polygon) → 400 | handler | area enforced |
| Flutter: one build, two fake `/config`s (food and courier) → different theme, terms, lifecycle and screens; courier shows no menu, food no weight field; both locales render | widget | runtime white-labelling + i18n |
| `platformctl` commands: `init`, `new-pack --from courier`, `new-module` (scaffold builds and its tests pass), `import --dry-run` (reports every bad row, writes nothing), `doctor` | CLI integration | buyer tooling works |
| Map tiles come from config; no default points at tile.openstreetmap.org | unit | tile policy respected |

### Phase E: product features

| Test | Type | Proves |
|---|---|---|
| Every payment adapter: hosted checkout; forged webhook signature → 401; the same webhook twice → one effect; refund; reconciliation totals | integration (gateway test mode) | money handling |
| Onboarding: invite or temporary password forces a reset; linked `refId` | handler | accounts |
| Console, agent, customer, admin flows: widget tests per screen plus the e2e core suite | widget + e2e | role apps |
| Notification templates render for every event × locale in every shipped pack (golden files) | unit | copy completeness |
| CSV export matches the job list filters | integration | admin |
| Accessibility guidelines (`meetsGuideline`) on every role app | widget | a11y |

### Phase F: package it for sale

| Test | Type | Proves |
|---|---|---|
| Fresh clone (see below) | CI + manual on three OSes | time to first run < 15 min |
| Upgrade: from the previous tag, with a customised pack and a buyer strategy in `ext/`, pull the new version → builds, e2e passes, no merge conflicts | scripted | ADR-0004 upgrade promise |
| License scan: every Go module, Dart package, image, font, icon and tile source is permissive or flagged | script | legal cleanliness |
| Repo hygiene grep: no university, coursework or personal references, no TODOs in shipped paths, no secrets (gitleaks on the fresh repo) | CI | code survives inspection |
| `/security-review` findings fixed or accepted in writing | review | security |

## Coverage targets

Line coverage for the packages that hold the logic buyers extend or depend
on, enforced in CI once each package exists:

| Packages | Target |
|---|---|
| `pkg/lifecycle`, `pkg/strategy/**`, `*/internal/domain` | 90% |
| `pkg/events/bus/**`, `pkg/auth`, `pkg/config`, `pkg/app` | 80% |
| `*/internal/api` (route-role matrices + handlers) | 70% |
| everything else | measured and reported, no gate |

Coverage is a floor, not the goal: a strategy at 90% with no invalid-input
rows in its table still fails review.

## Fresh-clone test

The first thing a buyer does is clone and run, so CI does it too, on every
release candidate:

1. Check out into an empty directory on a clean runner (no Go/Flutter caches).
2. Follow only `README.md`'s quick start, verbatim, via a script that
   parses its code blocks. If the README drifts from reality, this fails.
3. Assert that the seeded demo serves the gateway, that the core e2e suite
   passes for the default pack, and that the whole thing took < 15 minutes.
4. Run manually on Windows and macOS before each release, and record the
   times in `VERIFY.md` (Phase F).

## The release gate

A version may be sold only when **all** of these hold:

1. The check line passes on the release commit: Go build, vet, race tests,
   gofmt and lint, integration tests, the bus contract on both
   implementations, Flutter analyze and test, and coverage targets.
2. `./scripts/e2e.sh --pack <p>` passes the core suite and the pack
   scenarios for **every shipped pack**, on the `lite` profile, and once on
   `full`.
3. The fresh-clone test passes, with times recorded for Linux, Windows and
   macOS.
4. The upgrade test from the previous tag passes.
5. The license scan is clean, or every exception is approved in writing.
6. The repo hygiene checks pass, and the product ships from a fresh
   repository with a clean history.
7. `/security-review` is clean or its accepted findings are documented.
8. `dist/VERIFY.md` lists exactly what was run, where, and the results.
