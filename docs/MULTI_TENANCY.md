# Multi-tenancy

The platform is built to run **one brand per install**: one `config/brand.yaml`,
one database, one set of servers. That is the supported shape, and it's
the simplest to run and to reason about.

Some buyers want to host several brands (tenants) on one install and sell
it as a service. The data model has the first step for that, and this
page says what it is and what's left for you to build.

## What ships

- `users`, `merchants` and `jobs` each have a `tenant_id` column
  (`NOT NULL`, never empty, indexed).
- Every row gets the brand's `tenant_id` when it's created (`config/brand.yaml`,
  default `default`). The stores are built with it
  (`store.New(pool, d.Brand.TenantID)` in identity and merchant; the jobs
  API sets it on each job).
- Rows from an install older than the column were given `default` when
  the migration ran.

That's all. **Nothing reads `tenant_id` yet.** Queries don't filter on it,
tokens don't carry it, and `GET /config` serves one brand. Two tenants in
one database today would see each other's merchants and jobs.

## Changing `tenant_id` on a running install

Don't, unless you know why. New rows get the new value and old rows keep
the old one. Because nothing filters on it, nothing breaks, but the
column stops meaning anything useful. If you need to rename a tenant,
update the existing rows in the same maintenance window:

```sql
UPDATE identity.users     SET tenant_id = 'new' WHERE tenant_id = 'old';
UPDATE merchant.merchants SET tenant_id = 'new' WHERE tenant_id = 'old';
UPDATE jobs.jobs          SET tenant_id = 'new' WHERE tenant_id = 'old';
```

## Going multi-tenant: the work

In order, each step safe to ship on its own:

1. **Resolve the tenant per request.** Usually from the host name
   (`eats.example.com` → `eats`) at the gateway, which forwards it. Refuse
   requests for an unknown tenant.
2. **Put it in the token.** Add a `tenant` claim when identity issues a
   token, and check in `auth.Issuer.Middleware` that it matches the
   request's tenant. A token from one tenant must not work on another.
3. **Filter every query.** Every read and write in the identity, merchant
   and jobs stores gets `AND tenant_id = $n`. Do it in the store, not the
   handler, so a new handler can't forget it. Postgres row-level security
   is a good backstop once this is done.
4. **Carry it on events.** Add `tenantId` to the payloads in
   `pkg/events/events.go` (add a field; don't rename one), so consumers in
   dispatch, payment, notification and reporting can stamp and filter
   their own tables. Those tables need the column too (new migrations).
5. **One brand per tenant.** Load a brand file per tenant instead of one
   `BRAND_FILE`, and serve `GET /config` and `/brand/logo` for the
   request's tenant.

Write a test for each step that makes two tenants and proves neither can
see the other's data. That test is the feature.

## The alternative

Often simpler: **one install per tenant**. Separate databases (or
separate schemas) and separate processes give you isolation without
touching the code. The platform is small enough to run many copies of
on one machine. Choose shared tenancy only when the number of tenants
makes that too expensive to operate.
