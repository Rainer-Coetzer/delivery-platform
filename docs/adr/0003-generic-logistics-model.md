# ADR-0003: A generic logistics core; food delivery becomes one pack

**Status:** Accepted (amended 2026-09-25: one leg per job, see the end)
**Date:** 2026-09-24
**Deciders:** Maintainers

## Context

The platform must serve any business that moves something (or someone)
from A to B with a person doing the work: food and grocery, parcels,
pharmacy and retail, pickup-and-return (laundry, repairs), B2B freight, and
field service. Each client gets one deployment (ADR-0002). A **vertical
pack** (config plus templates) has to turn the same core into any of these
**without code changes**. Code is written only for genuinely new
capabilities, and each one becomes a reusable module.

Today's code is food-shaped end to end: restaurants, menu items, a kitchen
cache, an order state machine hardcoded in Go (and copied into Dart), and
driver deliveries with exactly one pickup and one dropoff.

## Decision

### 1. Core entities

```
Merchant (optional) ─┐
                     ▼
Customer ──creates──► Job ──has 1..n──► Stop (ordered: kind, location, window, proof required)
                      │  └─has 0..n──► JobLine (catalogue item | freeform package)
                      │  └─grouped in 1..n──► Leg (contiguous stops handed to one agent)
                      ▼
               Assignment (Agent ⇄ Leg) ── Agent ──has 1..n──► Vehicle (type, capacity, capabilities)
```

| Entity | Meaning | Key fields |
|---|---|---|
| **Job** | The unit of work a customer pays for | id, tenant_id, customer_id, merchant_id?, pack state, facts, price, currency |
| **Stop** | One place something happens | job_id, seq, kind (`pickup` \| `dropoff` \| `return` \| `waypoint` \| `service`), location, contact, time window, `proof` required (none \| photo \| signature \| pin \| id_check) |
| **Leg** | Stops handed to one agent in one go | job_id, from_seq, to_seq, dispatch trigger (a milestone) |
| **JobLine** | What is moved or done | catalogue_item_id? *or* description, quantity, weight_g, dims_mm, declared_value; unit price |
| **Merchant** | A business that originates or receives jobs; optional (courier is customer to customer) | name, location, timezone, hours, intake paused |
| **Agent** | The person doing the work (driver, courier, technician) | user_id, status, current location |
| **Vehicle** | What the agent works with | agent_id, type, capacity (kg, m³, pallets), capabilities (`cold_chain`, `id_check`, `hazmat`) |
| **Assignment** | An agent holding one leg | leg_id, agent_id, status, timestamps, proof refs |

**Legs are in the core from day one**, even though food and courier always
have exactly one. Pickup-and-return (laundry: customer to shop, then days
later shop to customer, often by different agents) and freight relays need
per-leg dispatch. Adding legs later would mean rewriting dispatch and the
assignment table; adding them now costs one table and a default of one leg
per job.

### 2. Configurable lifecycle, over a fixed milestone spine

A pack defines its own **states** (names, labels, order) in
`packs/<pack>/lifecycle.yaml`. The core cannot let packs rename *everything*,
though. Other modules must react to *meaning*: the kitchen starts when an
order is confirmed, dispatch starts when it's ready, refunds happen on
cancel. Today those reactions are keyed to hardcoded state names
(`PREPARING`, `READY`), which is exactly what a configurable lifecycle
breaks.

So the core defines a small, fixed set of **milestones**, and each pack
state maps onto at most one of them:

| Milestone | Core modules react by… |
|---|---|
| `created` | pricing, payment intent (if `payment.when: on_create`) |
| `confirmed` | catalogue: reserve stock and start prep; merchant queue shows it |
| `ready_for_dispatch` | dispatch: assign the first leg with `dispatch_on: ready_for_dispatch` |
| `in_transit` | tracking goes live |
| `completed` | payment capture (if `on_completion`), reporting, receipts |
| `cancelled` | compensation: refund, release stock, free the agent |

**Facts** are boolean flags that events set on a job (`paid`,
`merchant_accepted`, `approved`, `quote_accepted`). Transitions can require
them.

A transition declares exactly **one** trigger:

- `on: <event>`: a domain event, e.g. `merchant.job-ready`, `dispatch.stop-completed`
  (filtered by stop kind or "last dropoff").
- `by: [roles]`: a person acting through the API, e.g. a customer cancel or an
  admin manual step.
- `auto: true` with `when: [facts]`: fires as soon as the facts hold. This is
  how today's "CONFIRMED once paid **and** accepted" join is expressed.

Boot validation fails loudly when:
- a state is unreachable from the initial state
- a terminal state has exits
- a non-terminal state has no path to a terminal
- a transition has zero or two triggers, or names an unknown event, role or fact
- a milestone is required by an enabled module but mapped by no state
  (e.g. `dispatch` enabled but no `ready_for_dispatch`)
- more than one state maps to `completed` or `cancelled`

**Where this differs from the brief.** The brief says every transition names
the role *or* event allowed to trigger it. The code needs a third kind,
`auto` with fact guards. Today's lifecycle has a two-event join (payment +
restaurant) and an immediate step (CONFIRMED → PREPARING) that no single
event or role triggers.

### 3. Food concepts mapped onto the core

| Today (code) | Generic core | Becomes |
|---|---|---|
| `order` service, `orders` table | **jobs** module, `jobs.jobs` | core |
| `orders.status` + `validTransitions` (Go) + step list (Dart) | pack `lifecycle.yaml`; states served by `GET /config` | config |
| `orders.pickup_*` / `dropoff_*` columns | `jobs.stops` (seq 1 pickup at merchant, seq 2 dropoff) | core |
| `order_items` | `jobs.job_lines` (catalogue-backed) | core |
| `order_status_history` | `jobs.job_events` | core |
| `paid`, `restaurant_accepted` flags | job **facts** `paid`, `merchant_accepted` | core |
| surge, base fee constant | `pricing.Policy` `surge_tiers` + `flat` numbers in pack | config (strategy module) |
| `restaurant` service, `restaurants` table | **merchants** module | core (optional per pack) |
| `restaurants.timezone`, opening hours, `is_open` | merchant hours and intake pause | core |
| `menu_items`, stock, `CheckItems` | **catalogue** module | module |
| `restaurant_orders` kitchen cache, PREPARING, mark-ready, autopilot | merchant **work queue**: accept/reject/ready, keyed on milestones | core (merchants) |
| prep time (`AUTOPILOT_PREP_TIME`) | catalogue `prep_time` per merchant or item; autopilot demo flag | config |
| `delivery` service, `drivers` | **dispatch** module, `agents` + `vehicles` | core |
| `deliveries` (ASSIGNED/PICKED_UP/COMPLETED) | `assignments` per leg, with stop progress | core |
| `pending_assignments`, `cancelled_orders` tombstones | dispatch queue and tombstones, per leg | core |
| nearest-driver PostGIS query | `dispatch.Strategy` `nearest` | strategy module |
| 8.3 m/s ETA constant | `eta.Estimator` speed per vehicle type from pack | config |
| driver GPS simulator | demo module (`sim`), off unless the dev profile enables it | module |
| `payment` service, simulated 90% success | **payments** module; `payment.Provider` `simulated`, `cash`, later a real gateway | core + provider modules |
| notification `fmt.Sprintf` switch, "N$" | **notifications** module; `text/template` per event × locale **in the pack** | core + config |
| `customer` service: users, addresses, order history | **identity** (users, roles, auth) and **customers** (addresses, job history projection) | core |
| `admin` projections | **reporting** module | core |
| gateway | **edge**: auth, `GET /config`, WS, rate limits | core |

**Events are renamed with the model** (no client deployments exist, so no
dual-publishing):

| Today | Core |
|---|---|
| `orders.created` / `orders.status-changed` / `orders.cancelled` | `jobs.created` / `jobs.status-changed` (adds `milestone` and `facts`) / `jobs.cancelled` |
| `restaurant.order-accepted` / `-rejected` / `-ready` | `merchant.job-accepted` / `-rejected` / `-ready` |
| `restaurant.order-preparing` (never produced) | removed; the `confirmed` milestone covers it |
| `payments.*` | unchanged |
| `delivery.assigned` / `picked-up` / `completed` | `dispatch.assigned` / `dispatch.stop-completed` (per stop, with proof refs) / `dispatch.leg-completed` |
| `delivery.location` | `dispatch.location` |
| `users.registered`, `notifications.created` (never produced) | removed until something needs them |

One design change comes with the renames. **Dispatch listens to the jobs
module's `ready_for_dispatch` milestone, not to the merchant.** A courier job
has no merchant; today dispatch would never start one.

### 4. Capability modules vs configuration

**Modules** (code, optional, enabled per pack):
- `catalogue`: items, stock reservation, prep time, catalogue-backed pricing.
- `packages`: freeform lines with weight, dimensions and declared value, validated.
- `multistop`: more than two stops, several legs, round trips.
- `scheduling`: booked time windows; dispatch waits for the window instead of ASAP.
- `proof`: photo, signature, PIN or ID-check per stop, stored and linked to the stop.
- `documents`: files on a job (waybills, permits, customs forms).
- `capacity`: vehicle capacity against job size, used by the dispatch filter.
- `approval`: a merchant or admin gate before dispatch (sets the `approved` fact).
- Strategies: `pricing` (`flat`, `per_km`, `per_kg`, `zone_table`, `surge_tiers`,
  `quote`), `dispatch` (`nearest`, `least_loaded`, `capacity_aware`, `manual`,
  `open_offer`), `eta`, `payment.Provider`, `notify.Channel`.

**Configuration** (the pack or client file, no code):
- Which modules and strategies are on, and their numbers: rates, tiers, zones, speeds.
- The lifecycle: states, labels, milestones, transitions, facts.
- Stop templates per job type (food: merchant pickup → customer dropoff;
  laundry: customer → merchant, then merchant → customer as a second leg).
- Proof required per stop kind.
- Payment timing (`on_create`, `on_quote_accept`, `on_completion`, `invoice`)
  and allowed methods.
- Terminology per locale (`job: order`, `merchant: restaurant`,
  `agent: driver`), notification templates, and copy.
- Enabled roles.

**Rule for "module or config?"** If it needs new *data* (a new table or
column) or new *behaviour* (code that decides something), it's a module. If
it only picks between behaviours that already exist or supplies numbers and
words, it's config.

### 5. Naming in code and API

- **API:** `/jobs`, `/merchants`, `/catalogue/items`, `/agents`, `/assignments`.
  JSON fields are generic (`merchantId`, `agentId`, `stops[]`, `lines[]`).
- **No food nouns** outside `packs/food/`. A grep for
  `restaurant|menu|food|driver|kitchen|dish` outside that folder must return
  only justified hits (e.g. migration history, CHANGELOG).
- **Migrations preserve data.** The Phase C renames (`RENAME`, backfills of
  stops from the old pickup/dropoff columns) run as goose migrations on the
  module schemas that Phase B creates, and are tested against a database
  seeded with the pre-rename shape. The move from seven databases into one
  (Phase B) starts fresh instead, as ADR-0002 explains. No client data
  exists yet, so this is the rehearsal for when it does.

## Options considered

- **A. Keep food, bolt on a "courier mode" flag.** Rejected: every new
  vertical would add `if` branches through core code, which is the opposite
  of "new vertical = config".
- **B. Generic core with a fully free-form state machine** (states and
  transitions only). Rejected: modules would have to hardcode pack state
  names to react, which couples core code to packs again.
- **C. Generic core, pack lifecycle over a fixed milestone spine, facts for
  joins (chosen).** Packs get naming and flow freedom; the core keeps a
  stable contract to react to.
- **D. Workflow engine (e.g. Temporal).** Rejected for now: another stateful
  service per client (it breaks ADR-0002's small-VPS goal) for flows that a
  validated YAML state machine plus events already covers.

## Consequences

- **Easier:** a new vertical is `packs/<name>/` plus a client profile. The
  Flutter app renders states, labels and modules from `GET /config`, so it
  stops duplicating backend enums.
- **Harder:** the rename touches every service, event, API path and Flutter
  screen at once (Phase C). The milestone spine must stay small and stable;
  adding one is a core change that every pack inherits.
- **Later** (not built unless asked; listed in the product brief's "Later"):
  - relay jobs across several agents with hand-offs mid-leg
  - pooling several jobs into one route (route optimisation)
  - marketplace search across merchants
  - partial deliveries
  - multi-currency within one client
  - customs integrations

## Action items (Phase C)

1. [x] Schemas and migrations for jobs, stops, lines, facts; merchants;
   catalogue; agents (with vehicle capacity), assignments, with renames and
   backfills from today's tables. Legs: see the amendment below.
2. [x] `pkg/lifecycle`: YAML loader plus validator, with table-driven tests
   including invalid packs.
3. [x] Event renames in `pkg/events`; dispatch driven by the milestone.
4. [x] Strategy registries, selected by the pack.
5. [x] `packs/food` (reproduces today exactly), `packs/courier` (complete),
   and `packs/freight` (working basic; invoicing and quote pricing are
   Phase E).
6. [x] Terminology layer and the grep check (`tools/termcheck`).

## Amendment (Phase C, 2026-09-25): one leg per job, for now

Legs are not built. Every job is one leg, run by one assignment that visits
all its stops in order (multi-stop included). A second leg needs its own
dispatch trigger (laundry's return trip waits days for "ready to return"),
and the milestone spine has one `ready_for_dispatch`. Per-leg dispatch
means per-leg milestones, which is a change to this ADR's spine, not a
module; it is listed under Later with that reason. Nothing in the shipped
packs needs it. Vehicles are likewise one per agent (capacity and
capabilities on the agent) until a pack needs a fleet per agent.
