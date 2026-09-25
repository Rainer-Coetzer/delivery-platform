# Delivery Platform

A distributed food delivery platform built with **Go** microservices, **Kafka**, **PostgreSQL + PostGIS**, and a
**Flutter** client for web, Android and iOS. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

## Services

| Service | Port | Owns |
|---|---|---|
| [gateway](services/gateway) | 8080 | JWT auth, reverse proxy, WebSocket hub for live tracking |
| [identity](services/identity) | 8081 | Accounts (every role), addresses, job-history projection |
| [merchant](services/merchant) | 8082 | Merchants, catalogues, stock, accept/reject decisions |
| [jobs](services/jobs) | 8083 | The job lifecycle and the event contract |
| [payment](services/payment) | 8084 | Simulated payment authorization and refunds |
| [dispatch](services/dispatch) | 8085 | Agent assignment (PostGIS), tracking, location simulation |
| [notification](services/notification) | 8086 | In-app notifications from every domain event |
| [reporting](services/reporting) | 8087 | CQRS read-model reports (revenue, completion times, agent stats) |

The domain is generic (job, merchant, agent, assignment; ADR-0003). The
`food` pack names them order, restaurant, driver and delivery on screen.

## Running it

Prerequisites: Docker and Go 1.26+ (the seed and e2e tools are small Go
programs, so no `jq` or other CLI tools are needed).

There are two deployment shapes of the same code
([ADR-0002](docs/adr/0002-modular-monolith.md)), chosen with a compose profile:

| Profile | What runs | Use it for |
|---|---|---|
| `lite` | `cmd/platform` (every module in one process) + Postgres/PostGIS + Caddy. In-process events bus in Postgres. About 126 MiB idle. | a client's VPS; the default everywhere |
| `full` | the eight services + Kafka + Prometheus/Grafana/Jaeger | the microservice shape; Kafka work |

**One command, end to end.** This builds and starts the `lite` stack with
the demo autopilot on, seeds demo data, places an order through the gateway
and waits for `DELIVERED`:

```bash
./scripts/e2e.sh                    # lite
E2E_PROFILE=full ./scripts/e2e.sh   # microservices + Kafka
```

**Just running it:**

```bash
cp .env.example .env
docker compose --profile lite -f docker-compose.yml -f docker-compose.dev.yml up --build
./infra/seed/seed.sh
```

The gateway is on http://localhost:8080 (Caddy in `lite`, the gateway
service in `full`). If something already uses 8080, set
`GATEWAY_HOST_PORT=18080` and `GATEWAY_URL=http://localhost:18080`.

Without `-f docker-compose.dev.yml`, `DEMO_AUTOPILOT` is off. Orders then
stop at `PREPARING` until restaurant staff mark them ready and a driver taps
picked-up and delivered, which is the real-world flow.

| Setting | Default | Dev overlay |
|---|---|---|
| `DEMO_AUTOPILOT` | `false` | `true` |
| `AUTOPILOT_PREP_TIME` | `20s` | `5s` |
| `SIM_STEP_METERS` | `60` | `300` |
| `EVENTS_BUS` | `inprocess` (platform), `kafka` (services) | |
| `PLATFORM_MODULES` | all | |
| `POSTGRES_HOST_PORT` | `55432` | |
| `GATEWAY_HOST_PORT` | `8080` | |
| `SITE_ADDRESS` (Caddy) | `:80` (plain HTTP) | set to the client's domain for automatic HTTPS |

In the `full` profile, the UIs bind to 127.0.0.1: Kafka UI on :8090,
Grafana on :3000 (anonymous Viewer), Prometheus on :9090, Jaeger on :16686
(no tracing code yet).

Run the Flutter client: see [apps/delivery_app/README.md](apps/delivery_app/README.md)
for first-time platform setup (`flutter create`).

## Security model

- **The gateway is the only public port.** Backends have no host ports. The
  debug overlay `docker-compose.debug.yml` publishes them on `127.0.0.1`.
- **Every service verifies the JWT itself** and checks roles and ownership
  per route ([ADR-0001](docs/adr/0001-identity-propagation.md)). Identity
  never comes from request bodies.
- **Accounts.** Public sign-up creates customers only. An admin onboards
  merchants and agents: they create the merchant or agent, then its
  login with `POST /users` (`refId` links the two). The first admin comes
  from `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD`.
- **Prices are computed on the server** from the merchant's catalogue. The
  merchant re-checks item ownership and price on acceptance.
- **Live tracking** (`/ws?jobId=...&token=...`) is only for the job's
  customer, merchant, assigned agent or an admin.
- **Fail fast.** With any `PROFILE` other than `dev`, services refuse to boot
  if `JWT_SECRET` is missing, short or a placeholder, or if `ALLOWED_ORIGINS`
  is empty or has wildcards. The code default is *not* dev, so forgetting
  `PROFILE` lands on the strict side. See `.env.example`.

Demo logins after `./infra/seed/seed.sh` (all use `password123`, dev only):

| Role | Email |
|---|---|
| admin | `admin@example.com` (password `admin-dev-password`, from the dev overlay) |
| customer | `nangula@example.com` |
| merchant | `owner+alldaykitchen@example.com` (and one per seeded merchant) |
| agent | `agent+johannesshikongo@example.com` (and one per seeded agent) |

## Development

```bash
go build ./... && go vet ./... && go test ./... -race -count=1 && gofmt -l .

# Integration tests: need a PostGIS Postgres, e.g. the compose one
docker compose up -d --wait postgres
TEST_DATABASE_URL="postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable"   go test -tags=integration ./...
```

Integration tests skip when `TEST_DATABASE_URL` is unset, and each test gets
its own throwaway database (`pkg/db/dbtest`).

Each module lives under `services/<name>/`: its migrations, domain logic,
store, API and consumers, plus a `module.go` that describes it to
`pkg/app`. The same `module.go` runs standalone (`services/<name>/cmd`) or
inside `cmd/platform`. Shared code lives in `pkg/`: event types and the
`events/bus` (in-process and Kafka), the outbox, DB helpers, JWT auth, the
`app` service kit and observability. `pkg/events/events.go` is the single
source of truth for every topic and payload shape.

## Repo layout

```
cmd/platform    every module in one binary (the lite deployment)
pkg/            shared: app (service kit), events + events/bus, kafka, outbox, db, httpx, auth, obs, config
services/       one folder per module (see table above), each with module.go + cmd/
apps/delivery_app/  Flutter client
infra/          postgres init, kafka topic creation, seed data, prometheus/grafana config
scripts/e2e.sh  end-to-end order flow check (logic in tools/e2e)
tools/          seed and e2e programs (Go, stdlib only)
docs/           architecture and design notes
```
