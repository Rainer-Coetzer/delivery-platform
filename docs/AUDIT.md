# Delivery Platform: Audit

**Date:** 24 September 2026
**Scope:** every tracked file (103 files, ~8.7k lines): `pkg/`, all 8 services, `infra/`, `scripts/`, CI, and the Flutter app.
**Goal:** make the platform as tailorable and modular as possible, and find anything that blocks it from working today.
**How it was checked:** I read all the code. `gofmt -l` is clean. A full `go build` couldn't run in the audit sandbox because the module proxy was blocked, so I haven't compiled or run anything. That failure did confirm the toolchain mismatch in P0-1.

---

## TL;DR

The foundations are good: a transactional outbox, idempotent consumers through `processed_events`, a single event contract in `pkg/events`, PostGIS KNN with `SKIP LOCKED`, and pure domain packages that already have tests. That's the hard part of an event-driven system, and it's done right.

Three problems stand out:

1. **It can't work end to end yet.** The Go toolchain versions conflict, `scripts/e2e.sh` can never reach `DELIVERED`, and CI's `flutter test` has no test folder to run.
2. **Security depends on nobody calling the API directly.** Anyone can self-register as `admin`, the client sets item prices, and the backend services have no authz at all. They're also published on host ports.
3. **Everything you'd want to tailor is a hardcoded constant:** pricing, surge tiers, payment success rate, the dispatch rule, ETA speed, currency (`N$`), timezone (UTC), map centre (Windhoek), notification text, theme colours, and the order state machine. Each one is duplicated between Go and Dart.

The modularity work comes down to four moves:
- a **service kit** (`pkg/app`) so each `main.go` is about 20 lines
- **typed config plus a platform profile** (`config/profiles/*.yaml`) shared by the backend and the Flutter app
- **strategy interfaces with a registry** for every business rule
- **contract-first APIs** (the empty `api/` folder becomes OpenAPI + AsyncAPI, with generated Go and Dart code)

---

## P0: Broken or unsafe (fix first)

| # | Finding | Where | Why it matters |
|---|---|---|---|
| 1 | **Toolchain mismatch.** `go.mod` says `go 1.27.0`, but the Dockerfiles use `golang:1.23-alpine`, CI uses `go-version: "1.23"`, and the README says 1.23+. `pgx v5.11.0` alone needs Go ≥ 1.25. Official golang images set `GOTOOLCHAIN=local`, so they won't auto-upgrade. | `go.mod`, `services/*/Dockerfile`, `.github/workflows/ci.yml`, `README.md` | Every Docker build and the CI Go job fail. |
| 2 | **e2e can never pass.** Nothing moves an order past `PREPARING` without a person. `markReady` needs restaurant staff, and `picked-up`/`completed` need a driver tap. The script only polls. | `scripts/e2e.sh`, `services/restaurant/.../api.markReady`, `services/delivery/.../api.transition` | The only integration test always times out. |
| 3 | **Privilege escalation.** `POST /auth/register` accepts any `role` (including `admin`) and any `refId`. It's proxied through the gateway unauthenticated, and the Flutter register screen offers `admin` in a dropdown. The seed script depends on this. | `services/customer/.../api.register`, `apps/.../register_screen.dart` | Anyone can become admin, or claim someone else's restaurant or driver. |
| 4 | **Client-controlled prices.** `createOrder` totals `unitPriceCents` from the request body. Restaurant `CheckStock` never compares price or checks that the item belongs to that restaurant. Payment charges the resulting `TotalCents`. | `services/order/.../api.createOrder`, `services/restaurant/.../domain.CheckStock` | A customer can order anything for N$0.01. |
| 5 | **No authz behind the gateway.** The gateway only checks that the JWT is valid. `auth.RequireRole` is never used. No identity is forwarded (no `X-User-*` headers), and no service reads one. That leaves IDOR everywhere: `GET /orders?customerId=<anyone>`, editing any restaurant's menu, marking any delivery completed, reading any inbox, and any customer can open `/admin/*`. | `services/gateway/cmd/main.go`, every `internal/api` | Every authenticated user is effectively an admin. |
| 6 | **Services bypass the gateway.** Compose publishes ports 8081–8087 on the host, and `seed.sh` calls them directly with no auth. | `docker-compose.yml`, `infra/seed/seed.sh` | Even the gateway's JWT check is optional. |
| 7 | **Silent insecure defaults.** `JWT_SECRET` falls back to `dev-secret-change-me`. CORS and WebSocket origins are `*`. The WebSocket has no auth and streams driver GPS to anyone who has an orderId. Postgres roles are `SUPERUSER`. Grafana gives anonymous users Admin. | gateway `main.go`, `ws/hub.go`, `infra/postgres/init.sql`, compose | Fine for a demo only if it's gated behind a `PROFILE=dev` flag that fails fast anywhere else. |
| 8 | **CI Flutter job fails.** `flutter test` runs, but `apps/delivery_app/test/` doesn't exist. | `.github/workflows/ci.yml` | Red CI. |
| 9 | **Restaurant hours are evaluated in UTC.** `OpenAt(minutesSinceMidnight(env.Timestamp))`, and `events.New` stamps UTC. In Windhoek (UTC+2), a 09:00–22:00 restaurant accepts orders from 11:00 to 00:00 local. | `services/restaurant/.../consumers.handleOrderCreated` | Wrong accept/reject decisions. There's no timezone anywhere in the model. |
| 10 | **Cancelled orders keep drivers busy.** The delivery service doesn't consume `orders.cancelled`, so the driver stays `BUSY` and `pending_assignments` rows retry forever. | `services/delivery/cmd/main.go` (topics) | Drivers leak out of the pool. |

## P1: Correctness and reliability

- **Projection race across topics.** `orders.created` and `orders.status-changed` are separate topics, and Kafka only orders messages within one partition of one topic. The customer and admin projections `UPDATE ... WHERE order_id=$1` and silently affect 0 rows if the status event arrives first. **Fix:** check `RowsAffected()` and return a retryable error, or upsert with a status-rank guard, or move to a single `orders.events` topic per aggregate.
- **`restaurant.order-ready` can arrive before `CONFIRMED`** (the API allows `markReady` whenever the restaurant order is `ACCEPTED`, even if payment is pending). The order consumer's `Transition(READY)` fails, retries 3×, and gets dead-lettered, while the delivery service still dispatches a driver for an unpaid order.
- **Retries don't tell permanent errors from transient ones.** `ErrInvalidTransition` and decode errors get retried with backoff before landing in the DLQ. Add a `kafka.Permanent(err)` wrapper that goes straight to the DLQ, plus a DLQ replay tool (right now nothing reads `*.dlq`).
- **The consumer commits once per poll batch, not per record** (the comment says per record). That's fine for at-least-once delivery, but fix the comment. Offsets also get committed after `ctx` cancellation mid-batch without a check.
- **Outbox:** `partitionKey()` falls back to `EventID` for non-order events (random partitioning), and the comment references an `InsertWithKey` that doesn't exist. `OutboxLagSeconds` is declared but never set. Nothing cleans up sent rows (the table grows forever).
- **Dead contract entries:** `restaurant.order-preparing`, `users.registered` and `notifications.created` are never produced. `DeliveryProgressData.DurationSeconds` is never set. `payments.refunded` has no notification.
- **Notification recipients don't match.** Some notifications go to `order:<id>`, but the inbox API and the app only ever query `user:`/`restaurant:`, so those notifications are unreachable.
- **Surge pricing** uses open orders for *one restaurant* against a hardcoded `assumedFreeDrivers = 5`, and the fee is `int64(float64 × surge)` (float money, truncation).
- **Location simulator** calls `ListDrivers()` (every driver) once per active delivery, every 2s. That's O(N×M). Use `GetDriver(id)`.
- **HTTP hardening:** no `ReadHeaderTimeout` (slowloris), no `http.MaxBytesReader` on bodies, `/healthz` doesn't check the DB or Kafka (liveness and readiness are the same thing).
- **Migrations** run on every replica at boot with no advisory lock. `infra/kafka/topics.sh` duplicates `events.AllTopics` by hand, so they can drift.
- **Docs reference files that don't exist:** `docs/API-CONTRACT.md`, `docs/EVENTS.md`, `docs/SETUP.md`. Jaeger is deployed but there's no tracing code.

## P1: Flutter app

- **Role flows are incomplete.** A driver never sees their assigned delivery, and `pickedUp`/`completed` exist in the repository but no screen calls them. There's also no `GET /drivers/{id}/active-delivery` endpoint. Creating a driver profile never links `refId` back to the user, so a new driver is created on every login. Restaurant accounts can never get a `refId`, so the board always says "No restaurant profile linked".
- **The session isn't restored.** The token is persisted but the user isn't, so every app start goes to `/login`. There's no 401 or expiry handling, and the WebSocket doesn't reconnect.
- **Hardcoded values:** `N$` in 5 places, the Windhoek fallback coordinates in 2, `Colors.deepOrange` and friends, the OSM tile URL and `com.example` user-agent, the payment method list, and the status step list (a copy of the Go state machine).
- **Structure:** one 290-line `providers.dart` holds every repository. Screens call repositories directly. `AdminDashboardScreen` creates a `FutureBuilder` future inside `build` (it refetches on every rebuild). `CartController` mutates `CartLine.quantity` in place. `StateNotifier` is the legacy Riverpod API (use `Notifier`/`AsyncNotifier`).
- **Models are hand-written** and have already drifted (`Order` ignores `pickup`, `dropoff`, `paid`, `cancelReason`, `createdAt`).

---

## Modularity and tailorability review

### What's already modular (keep it)

- The service layout `cmd / internal/{api,consumers,domain,store,migrations}` is consistent across all 8 services.
- `pkg/events` is a single typed contract, and `events.Decode[T]` is clean.
- `outbox.Publisher` is an interface, and `db.Queryer` lets store functions run on a pool or a transaction.
- Domain logic is pure and tested (`order.Transition`, `SurgeMultiplierFor`, `CheckStock`, geo).

### What blocks tailoring

| Concern | Today | Lever to add |
|---|---|---|
| Service bootstrap | 8 copies of the same ~100-line `main.go` (env, migrate, pool, producer, relay, DLQ producer, consumer, CORS, router, graceful shutdown), each slightly different | `pkg/app` service kit (below) |
| Configuration | `env(key, fallback)` copied 8×, defaults baked in, never validated | `pkg/config`: one typed struct per service, env plus optional YAML, validated at boot, fail fast outside `dev` |
| Pricing | `baseDeliveryFeeCents = 1000`, surge tiers in a `switch`, `assumedFreeDrivers = 5` | `pricing.Policy` interface: `flat`, `surge_tiers` (tiers from config), `distance_based` |
| Payments | `rand < 0.9`, method strings | `payment.Provider` registry: `simulated{successRate}`, `cash`, then real ones later |
| Dispatch | always the nearest driver, vehicle type ignored | `dispatch.Strategy`: `nearest`, `least_loaded`, `vehicle_aware` |
| ETA | `8.3 m/s` constant | `eta.Estimator` with speed per vehicle type from config |
| Restaurant acceptance | hours + stock hardcoded in the consumer | `[]acceptance.Rule` chain (hours w/ tz, stock, price match, capacity/max open orders) |
| Order lifecycle | `validTransitions` map in Go, copied in Dart | Serve the state machine from the API (`GET /meta/order-states`) so the app renders it. Optionally load it from YAML. |
| Notifications | text built with `fmt.Sprintf` in a switch, `N$` hardcoded, console log as the "channel" | `notify.Channel` interface (console, email, SMS, push), `text/template` files per event and locale, money formatter from the profile |
| Locale / brand / region | UTC, `N$`, Windhoek coordinates, deep orange, "Delivery Platform" | **Platform profile** `config/profiles/<name>.yaml`: brand name + colours, currency code, locale, timezone, default map centre, tile URL, enabled roles, payment methods, feature flags. The gateway serves `GET /config` and the Flutter app themes itself from it at runtime. |
| Demo vs real | the simulator is always on, and there's no auto-progress | Feature flags: `SIMULATE_DRIVER_GPS`, `DEMO_AUTOPILOT` (auto-ready after prep time, auto-pickup/deliver on arrival). This also fixes P0-2. |
| Contracts | the `api/` folder is empty, and Dart models are hand-copied | `api/openapi.yaml` + `api/asyncapi.yaml` → `oapi-codegen` (Go server interfaces + types) and `openapi-generator dart-dio` (client). Generate `infra/kafka/topics.sh` from `events.AllTopics`. |
| Event evolution | the envelope has no version, source or correlation id | Add `schemaVersion`, `source`, `correlationId`/`traceparent` to `Envelope` |
| Deployment | 8 near-identical Dockerfiles, a copy-pasted compose block per service, `latest` tags | One `Dockerfile` with `ARG SERVICE`, a compose `x-go-service` anchor, profiles (`core`, `obs`, `web`), pinned image tags, a `Makefile`/`Taskfile` |
| Flutter structure | screens by role, one providers file | Feature-first: `lib/features/<feature>/{data,domain,presentation}`, `lib/core/{config,theme,money,router}`, and a role registry where each role declares its home route and routes, so roles can be switched on or off from the profile |

### Target shape of a service after the refactor

```go
func main() {
    app.Run(app.Spec{
        Name:       "order-service",
        Migrations: migrations.FS,
        Outbox:     true,
        Consumes:   consumers.Topics,          // []string, next to the handlers
        Handler:    consumers.New,             // func(deps app.Deps) kafka.Handler
        Routes:     api.New,                   // func(deps app.Deps) func(chi.Router)
        Workers:    nil,                       // e.g. delivery: sim.Retrier, sim.Locations
        Config:     &config.Order{},           // typed, validated, env + profile
    })
}
```

`app.Deps` carries the pool, logger, producer, config, the resolved strategies (pricing, payment, dispatch, notify) and `auth.Principal` middleware. After this, adding a service means adding a folder, and swapping a business rule is a config change.

---

## Recommended order

1. **Make it run (P0-1, 2, 8, 10):** align Go versions, add `DEMO_AUTOPILOT`, add a Flutter smoke test, and have delivery consume `orders.cancelled`. Exit criterion: `docker compose up && seed && e2e` passes and CI is green.
2. **Close the holes (P0-3…7, 9):** role allow-list on register, server-side pricing, the gateway forwards a signed principal (or services verify the JWT), `RequireRole` plus ownership checks, drop host ports for backends, fail-fast secrets, restaurant timezone.
3. **Foundation:** `pkg/config`, `pkg/app`, the single Dockerfile and compose anchors, and generated `topics.sh`.
4. **Pluggability:** the strategy interfaces and registry, the platform profile, and `GET /config`.
5. **Contracts:** OpenAPI/AsyncAPI in `api/`, codegen for Go and Dart, and a versioned envelope.
6. **Flutter:** restructure feature-first, profile-driven theme/currency/map, finish the driver and restaurant flows, restore the session, reconnect the WebSocket.
7. **Hardening and observability:** readiness checks, timeouts, body limits, the outbox lag metric, OTel tracing into the Jaeger that's already deployed, and a DLQ replay tool.


---

## Phase 0 re-validation (24 September 2026)

I re-checked every item against commit `966164b`. This time `go list` ran, so the module graph was available, but no Docker or e2e run happened.

**Summary:** all 10 P0 items and every P1 item are **confirmed**. None has already been fixed, and none is wrong. Two corrections and four new findings are below.

**Corrections**

- **P0-1:** the floor is **Go 1.26**, not 1.25. `golang.org/x/{crypto,sys,text}`, `goose/v3` and `client_golang` all declare `go 1.26.0`, so every dependency is satisfied by 1.26. `go.mod`'s `1.27.0` is only there because the local toolchain is 1.27.
- **P1 retry semantics:** an undecodable *envelope* already goes straight to the DLQ. The retry problem only affects errors inside handlers (payload decode, `ErrInvalidTransition`, missing rows).

**New findings**

| # | Finding | Where | Rank |
|---|---|---|---|
| P0-11 | **Kafka image likely unpullable.** Bitnami moved versioned tags off Docker Hub's `bitnami/*` repos in 2025, so `bitnami/kafka:3.7` may not exist. Switch to the official `apache/kafka` image, which runs KRaft by default. `kafka-init` then needs `/opt/kafka/bin/kafka-topics.sh`. | `docker-compose.yml` | P0, fix in Phase 1 (blocks "make it run") |
| P1 | **Unknown restaurant id never gets a decision.** `loadRestaurant` returns `ErrNoRows`, the event is retried and dead-lettered, and the order stays `CREATED` forever. It should emit `restaurant.order-rejected`. | `restaurant/consumers.handleOrderCreated` | P1 (Phase 2 with server-side pricing, or Phase 4 with the acceptance chain) |
| P1 | **Offsets are committed even if DLQ publish fails.** `deadLetter` only logs the failure, then `CommitUncommittedOffsets` runs, so the message is lost. The same happens to records skipped mid-batch when `ctx` is cancelled. | `pkg/kafka.Consumer` | P1 (Phase 4, with `Permanent`) |
| P2 | `provectuslabs/kafka-ui` is unmaintained, so use `kafbat/kafka-ui`. Several images are `:latest`. | `docker-compose.yml` | P2 (Phase 3, pinned tags) |

**Dangling doc references (P1 docs) confirmed:** `docs/API-CONTRACT.md` (models.dart, httpx.go, hub.go, order api.go, order domain.go), `docs/EVENTS.md` (topics.sh, order api.go), `docs/SETUP.md` (compose).

**Re-ranked Phase 1 scope:** P0-1 (Go 1.26), **P0-11 (Kafka image)**, P0-2 (autopilot), P0-10 (delivery consumes `orders.cancelled`), P0-8 (Flutter smoke test). The priority score is (Impact + Risk) × (6 − Effort). Everything else keeps the order the audit gave it.

---

## Status after Phases 1 and 2 (24 September 2026)

**All P0 items are fixed**, verified by `scripts/e2e.sh` against a clean `docker compose` stack. The run included 6 security smoke checks, and the order went CREATED → DELIVERED in 28s.

| # | Fix |
|---|---|
| P0-1 | Go 1.26 everywhere |
| P0-2 | `DEMO_AUTOPILOT` (restaurant + delivery), `docker-compose.dev.yml`, Go-based seed/e2e |
| P0-3 | Public signup is customer-only. Admin onboarding via `POST /users`. Bootstrap admin from env |
| P0-4 | Order prices from the restaurant catalogue. Restaurant re-checks ownership and price (`CheckItems`) |
| P0-5 | JWT verified in every service (ADR-0001). Route-role matrices and ownership checks everywhere |
| P0-6 | Only the gateway is published. `docker-compose.debug.yml` for local backend ports |
| P0-7 | Fail-fast `JWT_SECRET`/`ALLOWED_ORIGINS` outside dev. Authenticated WebSocket. No SUPERUSER. Grafana Viewer |
| P0-8 | Flutter smoke test (the app also had a compile error, now fixed) |
| P0-9 | Restaurant IANA timezone, embedded tzdata |
| P0-10 | Delivery consumes `orders.cancelled` (tombstone + advisory lock) |
| P0-11 | `apache/kafka:3.9.1` |

**P1 items fixed along the way:** READY racing ahead of payment (mark-ready now needs PREPARING), unknown restaurant rejected, `durationSeconds` set, simulator N+1, and the dangling doc references.

**Found and fixed during the stack runs:**
- `init.sql` used `CREATE DATABASE` inside `DO`, so first boot never worked.
- The Postgres healthcheck passed mid-init.
- First-boot checkpoint timeouts on slow volumes.
- Backend CORS headers duplicated the gateway's.
- Driver BUSY → AVAILABLE toggling.
- Unescaped ids in internal URLs.
- Unvalidated menu PATCH and order dropoff.

**Still open and scheduled:**
- A misconfigured service still runs its migrations before failing its config check (Phase 3, `pkg/app` validates first).
- Login brute-force is limited only by the gateway's 100 requests/min/IP (Phase 7).
- Tokens live 24h with no revocation (ADR-0001 "revisit").
- Surge pricing, float money and the `order:` recipients (Phase 4).
