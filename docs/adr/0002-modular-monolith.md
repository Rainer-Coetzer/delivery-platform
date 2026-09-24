# ADR-0002: One binary per client, modules inside, the bus behind an interface

**Status:** Accepted
**Date:** 2026-09-24
**Deciders:** Rainer Coetzer

## Context

The product changed. This is no longer one food-delivery app. It becomes a
white-label logistics platform that is **hosted once per client** on a small
VPS and billed by the month or per job. Three constraints follow:

- **Cheap to run per client.** Today's stack is 8 Go services, Kafka (a JVM),
  Postgres, Kafka UI, Prometheus, Grafana and Jaeger. Kafka alone wants about
  1 GB of RAM. The pilot target is **under 1 GB idle for everything**.
- **Reliable with real jobs and real money.** At-least-once delivery,
  idempotent consumers and a transactional outbox already exist and must
  survive the change (`pkg/outbox`, `pkg/db.InsertIfNew`).
- **Easy to operate for one person.** One process to deploy, watch and roll
  back beats nine.

What the code shows today:

- Service boundaries are clean. Each service owns `internal/{api,consumers,domain,store,migrations}`
  and talks to the others only through Kafka events or a few HTTP calls:
  order → restaurant catalogue, gateway → customer verify, gateway WS → order.
- Every service has its **own** `outbox` and `processed_events` tables, with
  unqualified table names in its SQL.
- HTTP paths don't collide across services (`/orders`, `/restaurants`,
  `/drivers`, and so on), except `/healthz` and `/metrics`.
- Every main is about 100 lines of the same bootstrap.

## Decision

1. **A single `cmd/platform` binary runs every enabled module in one
   process**, keeping today's module boundaries. Config picks the modules
   (`modules: [identity, customers, merchants, catalogue, jobs, payments, dispatch, notifications, reporting, edge]`, per ADR-0003).
2. **One Postgres database, one schema per module.** Each module gets its own
   connection pool with `search_path=<module>`, so its existing unqualified
   SQL, including its own `outbox` and `processed_events`, runs unchanged
   inside its schema. Goose runs per module with a per-schema version table,
   under a Postgres advisory lock.
3. **`events.Bus` is an interface** with two implementations that pass one
   shared contract test suite:
   - `inprocess` (the default): a module's outbox relay appends to a shared
     `bus.log` table (bigserial id, topic, partition, key, payload) instead of
     Kafka. A dispatcher per consumer group reads its partitions in id order,
     calls the handler, and commits `bus.offsets(group, partition, last_id)`
     after success. Partition = hash(key) mod N (N = 3, as today), so one
     job's events stay ordered and one stuck job only blocks its own
     partition. Retries, then the DLQ (`bus.dead_letters`), exactly as the
     Kafka consumer does now.
   - `kafka`: today's franz-go code behind the same interface, selected by
     `EVENTS_BUS=kafka`.
4. **Synchronous calls between modules stay HTTP-shaped.** In the monolith
   they go through an **in-memory `http.RoundTripper` into the platform's own
   router**: no socket, no port. Calls still run as the end user, so every
   module keeps enforcing its own authz (ADR-0001). The HTTP clients that
   exist today (`order/internal/catalogue`, gateway auth and WS access)
   switch transport only.
5. **The per-service `cmd/` entry points keep building.** Each one becomes
   about 20 lines calling the same module constructor with one module
   enabled and `EVENTS_BUS=kafka`. The microservice deployment stays
   *possible* and CI compiles it, but only the monolith is e2e-gated (see
   Consequences).
6. **Compose profiles:** `pilot` (platform binary, Postgres/PostGIS and Caddy)
   and `full` (today's services, Kafka and observability).

## Options considered

### Option A: keep 8 microservices + Kafka per client

| Dimension | Assessment |
|---|---|
| RAM idle | ~1.5–2.5 GB (Kafka JVM ~1 GB, 8 Go procs ~25 MB each, Postgres, UIs) |
| Ops per client | 11+ containers to watch, upgrade and back up |
| Reliability | Good, but more moving parts and a broker to babysit |
| Cost | Needs a 4 GB VPS at minimum |

**Pros:** no change, and it scales out if a client ever grows large.
**Cons:** you pay the cost of distribution for load no pilot client will
have. Kafka is the single biggest RAM and ops cost.

### Option B: modular monolith, in-process bus on Postgres (chosen)

| Dimension | Assessment |
|---|---|
| RAM idle | Target < 1 GB total, measured in Phase B |
| Ops per client | 3 containers (platform, Postgres, Caddy); one thing to deploy and roll back |
| Reliability | Same outbox, idempotency and DLQ guarantees; a cross-module write is still two transactions joined by an event |
| Cost | Fits a 1–2 GB VPS |

**Pros:** cheapest to run, one log stream, one binary to roll back. Postgres
is already the system of record, so the bus adds no new stateful component.
**Cons:** a Postgres-backed log handles thousands of events a minute, not
Kafka volumes. That's fine for single-client dispatch, but the ceiling is
real and noted below.

### Option C: monolith with direct function calls and no events

**Rejected.** Modules would call each other's stores, and the boundaries
that let a module move back out into its own service would erode within
weeks. The event contract (`pkg/events`) is the asset worth keeping.

## Where the code challenged the expected answer

- **"Outbox + Postgres dispatcher" alone isn't enough.** Several consumer
  groups read the same event, and each needs its own position plus
  at-least-once redelivery. Without a log and per-group offsets, a
  dispatcher either delivers once and loses the retry, or needs per-row
  bookkeeping for every consumer. The design above is a deliberately small
  Kafka look-alike: log, partitions, offsets, DLQ.
- **Keep the per-module outbox, add the log.** In one database, a module
  *could* write straight into `bus.log` in its own transaction and skip the
  relay hop. We don't, because the same module code must also run as a
  separate service with its own database (option 5), where only an outbox
  works. The extra hop costs one insert per event.
- **Loopback HTTP instead of direct function calls** between modules. It
  looks odd inside one process, but it keeps authz where it is today, and
  one code path works in both deployments. The in-memory RoundTripper removes
  the network cost.
- **"Per-service entry points still build" has a price.** Two deployment
  shapes means two things that can rot. We accept compile-level support plus
  the shared bus contract suite for the microservice shape, and do not run
  its e2e on every change. If it's never used, delete it at Phase F.

## Consequences

- **Easier:** hosting a client is `compose --profile pilot up` on one small
  VPS. Backups are one `pg_dump`. Logs, deploys and rollbacks are one binary.
- **Harder:** a runaway query in one module can starve others, since they
  share a database and a process. Mitigate with per-module pool size limits
  and statement timeouts.
- **Ceiling:** the Postgres log is sized for one client's dispatch volume
  (low thousands of events per minute). A client beyond that moves to the
  `full` profile (Kafka) without code changes, because the bus is an
  interface.
- **Revisit:** `bus.log` retention (prune rows every group has committed and
  that are older than N days, Phase F). Per-module statement timeouts. Whether
  the microservice shape still earns its CI minutes.

## Action items (Phase B)

1. [ ] `pkg/events/bus`: `Bus` interface; `inprocess` (log, offsets,
   dispatcher, DLQ) and `kafka` implementations; one contract suite run
   against both: per-key ordering, at-least-once after a crash, dedupe via
   `processed_events`, DLQ on permanent errors.
2. [ ] Module constructors: each service's wiring moves into a `Module`
   (migrations, routes, handlers, workers), used by both `cmd/platform` and
   the per-service `cmd/`.
3. [ ] Schema-per-module migrations with a per-schema goose table and an
   advisory lock. The monolith starts from fresh schemas: today's data sits
   in seven separate databases, which `ALTER ... SET SCHEMA` can't move
   between. No client data exists yet, so there's nothing to carry over.
   A `pg_dump` per service into its schema is the documented path if that
   ever changes.
4. [ ] In-memory RoundTripper for inter-module HTTP.
5. [ ] Compose `pilot` (platform, Postgres, Caddy) and `full` profiles;
   measure idle RAM and write the number down.
