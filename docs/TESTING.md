# Testing

What proves each improvement phase is done. The phases are defined in
[AUDIT.md](AUDIT.md).
A phase counts as done only when every test listed for it exists and passes,
and the run has been reported honestly. If a layer couldn't run (for example
Docker wasn't available), the phase summary has to say so.

## Layers

| Layer | Tool | Where | Runs in | Speed |
|---|---|---|---|---|
| Domain (pure logic) | `go test`, table-driven | `services/*/internal/domain`, `pkg/*` | every commit, CI | ms |
| HTTP handlers | `net/http/httptest` + fake stores | `services/*/internal/api/*_test.go` | every commit, CI | ms |
| Consumer / store integration | `go test -tags=integration`, real Postgres (and Kafka where needed) | `services/*/internal/{consumers,store}/*_integration_test.go` | CI job with Docker; locally when Docker is up | seconds |
| Stack e2e | `scripts/e2e.sh` against `docker compose` | repo root | local + CI compose job | ~1–2 min |
| Flutter widget | `flutter test` | `apps/delivery_app/test/` | every commit, CI | seconds |

**Rules**

- Domain tests are table-driven (a project convention). Every new rule or strategy
  ships with a table.
- Integration tests use the `integration` build tag. They skip with
  `t.Skip("docker not available")` rather than failing when there's no Docker.
  CI runs them in a job that has Docker, so a skip never hides a failure there.
- Integration tests use one Postgres container per package, and each test
  gets a fresh database. Tests never share rows.
- The e2e script exits non-zero with the last seen status on timeout. It never
  just prints "FAILED" and exits 0.
- The full check line is `go build ./... && go vet ./... && go test ./... -race -count=1 && gofmt -l .`,
  plus `flutter analyze && flutter test`.

## What exists today (baseline)

| Area | Tests | Gap |
|---|---|---|
| `order/domain` | transitions, `MaybeConfirm`, surge tiers | none for the money maths |
| `delivery/domain` | haversine, ETA, `StepToward` | none |
| `restaurant/domain` | **none** | `OpenAt` (incl. overnight), `CheckStock` |
| `payment/domain` | **none** | `Authorize` is untestable (global rand) |
| handlers, consumers, stores | **none** | everything |
| Flutter | **no `test/` folder** | CI's `flutter test` fails |
| e2e | script exists, **cannot pass** (nothing advances past `PREPARING`) | autopilot |

## Phase 1: Make it run

| Test | Type | Proves |
|---|---|---|
| CI Go job builds with the pinned version, and `docker build` succeeds for all 8 services | build | P0-1 toolchain aligned |
| `docker compose --profile core up` → all healthchecks green | smoke | the stack boots (including the Kafka image) |
| `scripts/e2e.sh` with `DEMO_AUTOPILOT=true` reaches `DELIVERED` within 90s | e2e | P0-2 |
| `restaurant/domain`: autopilot due-time calculation (`ReadyAt(acceptedAt, prep)`) table | domain | autopilot rule is pure and tested |
| `delivery/consumers`: `orders.cancelled` → driver back to `AVAILABLE`, pending row deleted, delivery `CANCELLED`; same event twice → one effect | integration | P0-10 plus idempotency |
| `apps/delivery_app/test/smoke_test.dart`: app boots to the login screen inside a `ProviderScope` | widget | P0-8 |

## Phase 2: Secure by default

| Test | Type | Proves |
|---|---|---|
| `POST /auth/register` with `role: admin` / `refId` → 400; with no role → customer | handler | P0-3 |
| `auth` middleware table: missing/expired/wrong-alg/tampered token → 401; valid → principal in ctx | unit (`pkg/auth`) | the identity mechanism chosen in the ADR |
| Forged `X-User-*` or principal header from a client is stripped/rejected | handler (gateway) | header spoofing is closed |
| Route-role matrix: for every route, each role gets the expected 2xx/403. Table lives next to the router | handler | P0-5 `RequireRole` everywhere |
| Ownership: customer A `GET /orders?customerId=B` → 403; restaurant X edits Y's menu → 403; driver completes someone else's delivery → 403 | handler | P0-5 IDOR |
| Order create ignores client `unitPriceCents`/names; total = catalogue price × qty + fee | handler + integration | P0-4 |
| Restaurant rejects items from another restaurant and price mismatches | domain (`acceptance`) | P0-4 |
| WebSocket: no token → 401; another customer's order → 403; own order → receives events | handler (gateway) | P0-7 |
| Boot with `PROFILE=prod` and weak/missing `JWT_SECRET` or `*` origins → process exits non-zero with a named error | unit (`pkg/config`) | fail fast |
| `OpenAt` table in `Africa/Windhoek` and a second zone, including across midnight and a DST zone | domain | P0-9 |
| `seed.sh` works with backend host ports removed (goes through the gateway as admin) | e2e | P0-6 |
| Postgres service roles are not superuser (`SELECT rolsuper`) | integration | least privilege |

## Phase 3: Foundation

| Test | Type | Proves |
|---|---|---|
| `pkg/config`: env overrides YAML; missing required key → error naming the key; secrets redacted in the startup log | unit | typed, validated config |
| `pkg/app`: `/healthz` 200 while DB is down; `/readyz` 503 while DB or Kafka is down, 200 when both are up | integration | liveness vs readiness |
| `pkg/app`: oversized body → 413; `ReadHeaderTimeout` set | handler | HTTP hardening |
| Two replicas migrating concurrently → one applies, one waits, no error | integration | advisory lock |
| Topic creation reads `events.AllTopics`, and a test asserts that no hand-written topic list exists elsewhere | unit | one topic list |
| e2e still passes after every `main.go` moves to `app.Run` | e2e | no regression |

## Phase 4: Pluggable rules and profile

| Test | Type | Proves |
|---|---|---|
| Registry: unknown strategy name → boot error listing the valid names | unit per registry | config-selected strategies |
| `pricing`: `flat`, `surge_tiers`, `distance_based` tables. Integer maths only, rounding rule explicit | domain | pricing policy + no float money |
| `payment`: `simulated{successRate}` with an injected RNG seed (deterministic), `cash` | domain | testable payments |
| `dispatch`: `nearest`, `least_loaded`, `vehicle_aware` over a fixed driver set | domain | dispatch strategies |
| `eta`: per-vehicle speed table | domain | ETA estimator |
| `acceptance` chain: each rule alone, then chain order and first-failure reason | domain | acceptance rules |
| `notify`: every event × locale template renders with no missing keys; money uses the profile formatter | unit (golden files) | templates |
| Both profiles (`windhoek.yaml` and the second one) load and validate; `GET /config` returns only public fields | unit + handler | profile, no secrets leak |
| A grep-style test fails the build if `N$`, `Windhoek` coordinates or colour hex values appear outside `config/profiles/` | unit | nothing hardcoded |
| Projection race: status-changed before created → retried, not silently dropped; final row correct | integration | P1 projection race |
| `kafka.Permanent(err)` → DLQ on first attempt; transient error → retried | unit (`pkg/kafka`) with a fake client | P1 retry semantics |
| Outbox partition key for `delivery.location`/`users.registered` is the entity id; lag gauge set | unit + integration | P1 outbox |
| Notification recipients are always `user:`/`restaurant:`/`driver:` and the inbox finds them | integration | P1 recipients |
| Simulator makes one driver lookup per active delivery (fake store counts calls) | unit | P1 N+1 |

## Phase 5: Contracts

| Test | Type | Proves |
|---|---|---|
| CI regenerates Go + Dart code and fails on `git diff --exit-code` | CI check | generated code is fresh |
| Every topic in `events.AllTopics` appears in `asyncapi.yaml`, and the reverse | unit | spec ↔ code parity |
| Sample payload per topic validates against the AsyncAPI JSON schema | unit | payload shapes |
| Consumer decodes a v1 envelope and a v2 envelope identically | unit | envelope compatibility |
| `correlationId` survives HTTP → outbox → Kafka → consumer log | integration | propagation |

## Phase 6: Flutter

| Test | Type | Proves |
|---|---|---|
| Role router: each enabled role lands on its home; a disabled role (from `/config`) is hidden | widget | role registry |
| Theme and money formatting come from a fake `/config` (two profiles, two currencies) | widget | profile-driven UI |
| Cart: add, remove, switch restaurant clears the cart, totals in minor units | unit + widget | cart logic |
| Tracking screen: fake WebSocket stream drives the stepper; disconnect → reconnect with backoff | widget | live tracking |
| Session restore: stored token + `/me` → home without login; 401 → back to login | widget | session handling |
| Driver: active delivery card → pickup → complete calls the right endpoints | widget with fake repo | driver flow |
| Accessibility: tap targets ≥ 48dp and semantics labels on icon buttons (`meetsGuideline`) | widget | a11y review findings |

## Phase 7: Hardening and handover

| Test | Type | Proves |
|---|---|---|
| A trace for one order spans gateway → order → Kafka → restaurant in Jaeger | e2e (manual check, screenshot in the PR) | OTel across Kafka |
| DLQ replay command re-publishes N messages and they're consumed once | integration | replay tool |
| Outbox cleanup deletes only sent rows older than the retention | integration | cleanup |
| Grafana dashboards provision without errors (API check after compose up) | smoke | dashboards |
| `add-service` skill scaffolds a service that builds and passes `/readyz` in compose | e2e | project skills work |
