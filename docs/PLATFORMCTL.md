# platformctl

The tool for setting up, checking and extending your platform. Run it from
the repository root, with Go installed:

```bash
go run ./cmd/platformctl <command>        # or: go build -o platformctl ./cmd/platformctl
```

Every command says what it changed. None overwrites a file it didn't
create unless you pass `--force`, and then the old file is kept as
`<file>.bak`. Commands and flags are a stable surface (ADR-0004), so
scripts can rely on them. `platformctl <command> -h` lists a command's
flags.

## init

Sets up `config/brand.yaml` and `.env` from a few questions: name, pack,
colour, currency, timezone, map centre, default language and the first
admin's email.

```bash
platformctl init                                   # asks
platformctl init --yes --name "Acme Couriers" --pack courier \
  --currency ZAR --symbol R --timezone Africa/Johannesburg \
  --centre=-26.20,28.04 --admin-email ops@acme.example   # for scripts
```

- `.env` is `.env.example` with a fresh `JWT_SECRET` and a generated
  first-admin password. The password is printed once. Change it after
  the first sign-in.
- `--prod` sets `PROFILE=prod` and asks for the app's web address
  (`ALLOWED_ORIGINS`). The brand it writes uses OpenStreetMap tiles, which
  production refuses, so choose a provider next (`docs/MAPS.md`).
- The brand file is loaded with the server's own rules before anything is
  written.

## doctor

Runs the checks the server makes when it starts, one at a time, so you see
every problem at once: the tools (docker, go, flutter), `.env`, the JWT
secret, allowed origins, the first admin, the brand file, the pack, its
strategies, whether the brand fits the pack, and file storage.

```bash
platformctl doctor               # exit 1 if anything would stop the platform starting
platformctl doctor --skip-tools  # configuration only
platformctl doctor --connect     # also connect to DATABASE_URL, if set
```

It reads `.env` the way compose does. The shell's own variables win, and
`PROFILE` defaults to `dev`, as in `docker-compose.yml`.

## new-pack

```bash
platformctl new-pack acme --from courier
```

Copies a pack to `packs/acme/`, including its demo data, renames it, and
loads it with the server's rules. Then edit its `pack.yaml` and
`lifecycle.yaml`, set `pack: acme` in `config/brand.yaml`, and rebuild
(packs are built into the binary). `PACK=acme ./scripts/e2e.sh` runs its
demo job.

## new-module

```bash
platformctl new-module returns          # the backend module
platformctl new-module returns --app    # and its app screen
```

Writes `ext/returns/`: a working module with its own database schema,
routes under `/ext/returns` (admins only), a pure domain package with a
table test, a store and a route-role test. It registers the module in
`cmd/platform/extensions.go`. With `--app`, it also writes
`apps/delivery_app/lib/ext/returns/` and registers it in
`lib/extensions.dart`, where it shows as a link on the admin overview.
Everything it writes is yours: upgrades don't touch `ext/` or the two
extension files. `ext/returns/README.md` covers the project's rules: identity
comes from the token, modules talk over HTTP or events, and state changes
go through the outbox.

Modules run in the platform binary (compose `lite`). Running one as its own
service in the `full` profile isn't generated yet.

## import

```bash
platformctl import merchants merchants.csv --dry-run   # check, send nothing
platformctl import merchants merchants.csv             # send, as an admin
platformctl import catalogue items.csv
platformctl import agents agents.csv
```

The first row names the columns, in any order. `platformctl import -h`
lists them. An unknown column is an error, so a typo can't leave a field
quietly empty. Each row is checked before anything is sent:
- required fields,
- points and service area,
- `HH:MM` times and timezones,
- prices in the brand's currency (`65.00` or `65,00`, never more decimals
  than the currency has),
- whole numbers.

Rejected rows are written to `<file>.rejected.csv` with the reason in an
`error` column: fix that file and import it. Any rejection makes the
command exit 1.

The import goes through the gateway's API, so every server rule applies.
It signs in as an admin: `--email` (default `BOOTSTRAP_ADMIN_EMAIL`), with
the password in `PLATFORMCTL_PASSWORD` (default `BOOTSTRAP_ADMIN_PASSWORD`),
at `--gateway` (default `GATEWAY_URL`). Rows that already exist
(merchants and agents by name, items by name within their merchant) are
skipped, so a re-run is safe.

## brand-app

```bash
platformctl brand-app --bundle-id com.acme.deliveries --icon icon.png --run
```

Puts the brand on the app's native shells: the name in the web manifest
and page title, the Android label and the iOS display name, and the
bundle id (Android `applicationId`, iOS `PRODUCT_BUNDLE_IDENTIFIER`). It
writes:
- `config/app.json`, for `flutter build ... --dart-define-from-file`,
  with `APP_ID` and, with `--api-url`, `API_BASE_URL`,
- `config/app_icons.yaml` and `config/app_splash.yaml`, for
  `flutter_launcher_icons` and `flutter_native_splash`. `--run` runs them.

- The icon must be a square PNG of at least 512 px (the stores ask for
  1024). The brand's logo is used if it is a PNG.
- Platform folders come from `flutter create`
  (`apps/delivery_app/README.md`). brand-app edits the ones that exist
  and lists the rest.
- The theme, words, currency and map need none of this: the app reads them
  from `GET /config` at runtime.
