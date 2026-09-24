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

Prerequisites: Docker, Go 1.26+ (for local dev outside containers), and
`jq` if you want to run `scripts/e2e.sh`.

```bash
cp .env.example .env
docker compose up --build
```

Wait for every service to report healthy (`docker compose ps`), then seed
some demo data:

```bash
./infra/seed/seed.sh
```

Useful UIs while it's running:

- Gateway: http://localhost:8080
- Kafka UI: http://localhost:8090
- Grafana: http://localhost:3000 (anonymous admin access)
- Prometheus: http://localhost:9090
- Jaeger: http://localhost:16686

Run the end-to-end check (places an order and waits for it to reach
`DELIVERED`):

```bash
./scripts/e2e.sh
```

Run the Flutter client — see [apps/delivery_app/README.md](apps/delivery_app/README.md)
for first-time platform setup (`flutter create`).

## Development

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .   # should print nothing
```

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
scripts/e2e.sh  end-to-end order flow check
docs/           architecture and design notes
```
