# Delivery Platform

A distributed food delivery platform built with **Go** microservices, **Kafka**, **PostgreSQL + PostGIS**, and a
**Flutter** client for web, Android and iOS. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

## Services

| Service | Port | Owns |
|---|---|---|
| [gateway](services/gateway) | 8080 | JWT auth, reverse proxy, WebSocket hub for live tracking |
| [customer](services/customer) | 8081 | Accounts (every role), addresses, order-history projection |
| [restaurant](services/restaurant) | 8082 | Menus, stock, accept/reject decisions |
| [order](services/order) | 8083 | The order state machine and Kafka event contract |
| [payment](services/payment) | 8084 | Simulated payment authorization and refunds |
| [delivery](services/delivery) | 8085 | Driver assignment (PostGIS), tracking, location simulation |
| [notification](services/notification) | 8086 | In-app notifications from every domain event |
| [admin](services/admin) | 8087 | CQRS read-model reports (revenue, delivery times, driver stats) |

## Running it

Prerequisites: Docker and Go 1.26+ (the seed and e2e tools are small Go
programs, so no `jq` or other CLI tools are needed).

**One command, end to end.** This builds and starts the stack with the demo
autopilot on, seeds demo data, places an order and waits for `DELIVERED`:

```bash
./scripts/e2e.sh
```

**Just running it:**

```bash
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build   # autopilot on
./infra/seed/seed.sh
```

Without `-f docker-compose.dev.yml`, `DEMO_AUTOPILOT` is off. Orders then
stop at `PREPARING` until restaurant staff mark them ready and a driver taps
picked-up and delivered, which is the real-world flow.

| Flag | Service | Default | Dev overlay |
|---|---|---|---|
| `DEMO_AUTOPILOT` | restaurant, delivery | `false` | `true` |
| `AUTOPILOT_PREP_TIME` | restaurant | `20s` | `5s` |
| `SIM_STEP_METERS` | delivery | `60` | `300` |
| `POSTGRES_HOST_PORT` | postgres | `55432` | |

Useful UIs while it's running:

- Gateway: http://localhost:8080
- Kafka UI: http://localhost:8090
- Grafana: http://localhost:3000 (anonymous access, dev only)
- Prometheus: http://localhost:9090
- Jaeger: http://localhost:16686 (deployed, but no tracing code yet)

Run the Flutter client: see [apps/delivery_app/README.md](apps/delivery_app/README.md)
for first-time platform setup (`flutter create`).

## Security model

- **The gateway is the only public port.** Backends have no host ports. The
  debug overlay `docker-compose.debug.yml` publishes them on `127.0.0.1`.
- **Every service verifies the JWT itself** and checks roles and ownership
  per route ([ADR-0001](docs/adr/0001-identity-propagation.md)). Identity
  never comes from request bodies.
- **Accounts.** Public sign-up creates customers only. An admin onboards
  restaurants and drivers: they create the restaurant or driver, then its
  login with `POST /users` (`refId` links the two). The first admin comes
  from `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD`.
- **Prices are computed on the server** from the restaurant's catalogue. The
  restaurant re-checks item ownership and price on acceptance.
- **Live tracking** (`/ws?orderId=...&token=...`) is only for the order's
  customer, restaurant, assigned driver or an admin.
- **Fail fast.** With any `PROFILE` other than `dev`, services refuse to boot
  if `JWT_SECRET` is missing, short or a placeholder, or if `ALLOWED_ORIGINS`
  is empty or has wildcards. The code default is *not* dev, so forgetting
  `PROFILE` lands on the strict side. See `.env.example`.

Demo logins after `./infra/seed/seed.sh` (all use `password123`, dev only):

| Role | Email |
|---|---|
| admin | `admin@example.com` (password `admin-dev-password`, from the dev overlay) |
| customer | `nangula@example.com` |
| restaurant | `owner+alldaykitchen@example.com` (and one per seeded restaurant) |
| driver | `driver+johannesshikongo@example.com` (and one per seeded driver) |

## Development

```bash
go build ./... && go vet ./... && go test ./... -race -count=1 && gofmt -l .

# Integration tests: need a PostGIS Postgres, e.g. the compose one
docker compose up -d --wait postgres
TEST_DATABASE_URL="postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable"   go test -tags=integration ./...
```

Integration tests skip when `TEST_DATABASE_URL` is unset, and each test gets
its own throwaway database (`pkg/db/dbtest`).

Each service is self-contained under `services/<name>/` (its own
migrations, domain logic, store, API and Kafka consumer) and shares common
code from `pkg/` — event types, the Kafka client, the outbox pattern, DB
helpers, JWT auth and observability. `pkg/events/events.go` is the single
source of truth for every Kafka topic and payload shape.

## Repo layout

```
pkg/            shared Go module: events, kafka, outbox, db, httpx, auth, obs
services/       one folder per microservice (see table above)
apps/delivery_app/  Flutter client
infra/          postgres init, kafka topic creation, seed data, prometheus/grafana config
scripts/e2e.sh  end-to-end order flow check (logic in tools/e2e)
tools/          seed and e2e programs (Go, stdlib only)
docs/           architecture and design notes
```
