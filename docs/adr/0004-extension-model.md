# ADR-0004: How buyers extend the platform, and what we promise not to break

**Status:** Accepted
**Date:** 2026-09-25
**Deciders:** Rainer Coetzer

## Context

The platform is sold as source code. A buyer's developer rebrands it,
configures a vertical, and then extends it: a local payment gateway, a
pricing rule, sometimes a whole new capability. Then we ship a new release,
and they want it too.

Two failure modes kill a source-code product:

1. **Extending means editing our files.** Every edit becomes a merge conflict
   on the next release, so buyers stop upgrading and blame the product.
2. **Nothing is declared stable.** Buyers build on internals, and any
   refactor breaks them.

So the product needs three clearly separated ways to change it, a hard line
between the files we own and the files the buyer owns, and a written list of
what semver covers.

## Decision

### Three tiers, cheapest first

| Tier | What a buyer does | Examples | Code? |
|---|---|---|---|
| **1. Configure** | Edit YAML they own | brand, currency, locales, the active pack, lifecycle states and labels, terminology, strategy choice and its numbers (rates, tiers, zones, speeds), enabled modules and roles, notification templates | none |
| **2. Implement an interface** | Add a package that implements one interface and registers itself | a pricing rule, a dispatch strategy, an ETA model, a payment gateway, a notification channel | one package, one interface |
| **3. Add a module** | Add an `app.Definition` (routes, consumers, migrations, workers), usually scaffolded by `platformctl new-module` | a new capability, e.g. "returns inspection" or "cold-chain temperature logging" | a module |

If config can't express something (tier 1), implementing one interface
(tier 2) must be enough for everything in the table. A new module (tier 3)
is only for new data or new behaviour, which is the same rule ADR-0003 uses
for its own capability modules.

### Registries (tier 2)

Each strategy kind has a small registry in `pkg/strategy/<kind>`
(`pricing`, `dispatch`, `eta`, `payment`, `notify`):

```go
// in the buyer's own package, e.g. ext/pricing/fuelsurcharge
func init() {
    pricing.Register("fuel_surcharge", func(p strategy.Params) (pricing.Policy, error) { ... })
}
```

- A pack selects a strategy by name and passes params (`pricing: {name: fuel_surcharge, params: {...}}`).
- At boot, an unknown name is an error that lists the registered names.
- Params decode into the strategy's own typed struct. Unknown keys are an
  error, so a typo in a rate can't be silently ignored.
- Built-in strategies register the same way. Nothing is special-cased, so a
  buyer's strategy is a peer of ours.

### Who owns which file

| Owned by the product (replaced on upgrade) | Owned by the buyer (never touched by an upgrade) |
|---|---|
| `pkg/**`, `services/**`, `cmd/**` except the file on the right | `cmd/platform/extensions.go` (blank imports that register their strategies and modules) |
| `packs/food`, `packs/courier`, `packs/freight` (reference packs) | `packs/<their-pack>/` (made by `platformctl new-pack <name> --from courier`) |
| shipped migrations | `ext/**` (their strategies and modules) and their modules' own migrations |
| `apps/delivery_app/lib/**` except the buyer extension point | `config/**` (brand and environment) |
| | `apps/delivery_app/lib/extensions.dart` (the app's extension point: their `AppModule`s) and `apps/delivery_app/lib/ext/**` (those modules) |

Because the buyer's changes live only in buyer-owned paths, a release that
changes only product-owned paths merges without conflicts. Buyers who edit
product-owned files do so knowingly, at their own upgrade cost. The docs
say so plainly.

### Stable vs internal

**Stable** (semver: breaking changes only in a major release, with upgrade
notes):

- the config schemas: `pack.yaml`, `lifecycle.yaml`, the brand file. Each
  carries `schema: <n>`, and a config at an older schema version is
  migrated or rejected with instructions, never misread
- strategy interfaces and `Register` functions in `pkg/strategy/*`
- `app.Definition`, `app.Module`, `app.Deps`, `events.Bus`, the event
  envelope, and every topic payload in `pkg/events` (add fields, never
  rename or remove them within a major)
- the milestone set (ADR-0003)
- the public REST API and the WebSocket message shapes
- `platformctl` commands and flags
- the app's `AppModule` interface (`apps/delivery_app/lib/core/module.dart`;
  it grows only by members with defaults)

**Internal** (may change in any release):

- `services/*/internal/**`
- `pkg/db` internals and SQL schemas: buyers read them through the API or
  events, never with direct queries from their modules
- the Flutter app's internals, except its documented extension point (Phase D)

Go's `internal/` directories enforce most of this at compile time. The rest
is written down.

### Upgrade promise

- **Patch** (x.y.Z): fixes only, drop-in.
- **Minor** (x.Y.0): new features. Stable surfaces only grow, and config
  without new keys keeps working.
- **Major** (X.0.0): may break stable surfaces. Ships with `UPGRADING.md`
  and, where possible, an automatic config migration
  (`platformctl migrate-config`).
- Database migrations are append-only and run automatically at boot
  (ADR-0002), so upgrading is: pull, rebuild, restart.
- Phase F's release gate tests this. A scripted upgrade from the previous
  tag, with a customised pack and a buyer strategy in place, must build, pass
  the e2e and show no merge conflicts.

## Options considered

- **A. Fork-and-edit (no extension points).** Simplest to ship. Every buyer
  customisation conflicts with every release, and buyers stay on the version
  they bought. Rejected.
- **B. Go plugins (`plugin` package) or out-of-process plugins.** Go plugins
  need identical toolchains, don't work on Windows and are fragile across
  versions. Out-of-process plugins add RPC and deployment complexity to a
  product whose selling point is "one binary". Rejected.
- **C. Compile-time registries plus buyer-owned files (chosen).** The buyer's
  code is compiled in, with full type safety and no runtime loading, and
  upgrades stay conflict-free because ownership is split by path.

## Consequences

- **Easier:** the three extension examples buyers will try first (pricing
  rule, payment gateway, notification channel) are each one package, and
  `docs/EXTENDING.md` (Phase F) shows each as a worked example.
- **Harder:** we must keep the stable list honest. A contract test per
  stable surface (config schema versions, strategy registration, event
  payload compatibility) goes into the release gate.
- **Revisit:** whether to publish stable packages as a separate Go module
  with its own version, if buyers start vendoring only parts.
