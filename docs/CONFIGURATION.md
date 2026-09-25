# Configuration

Two kinds of file configure a platform, and both are yours:

- `config/brand.yaml`: who the platform is and where it works (this page).
- `packs/<name>/pack.yaml` and `lifecycle.yaml`: what it does (the pack).
  Start one with `platformctl new-pack <name> --from courier`.

Both are checked when the server starts. A mistake stops it with the file,
the field and the reason, rather than showing up later as a wrong colour or
a refused payment. Unknown fields are errors too, so a typo can't be
silently ignored.

Environment variables (`.env`) hold what differs between machines or is
secret: `JWT_SECRET`, `DATABASE_URL`, `ALLOWED_ORIGINS`, `PROFILE`,
`BLOB_DIR`, and any `map.tiles.key_env` key. See `.env.example`.

## `config/brand.yaml`

`BRAND_FILE` points at it (default `config/brand.yaml`; the Docker image
sets `/config/brand.yaml`, and compose mounts your `config/` there, so an
edit needs a restart, not a rebuild).

| Field | Required | Meaning |
|---|---|---|
| `schema` | yes | `1`. A file at another version is refused with instructions, never misread |
| `name` | yes | the platform's name, shown in the app |
| `tagline` | no | a line under the name |
| `logo` | no | a `.png`, `.jpg`, `.webp` or `.svg` next to `brand.yaml`, served at `/brand/logo` |
| `colours.primary` | yes | `#RRGGBB`; the app builds its palette from it |
| `colours.secondary`, `.background`, `.text` | no | `#RRGGBB`; derived from the primary when left out |
| `font` | no | a font family the app bundles; empty uses the platform default |
| `currency.code` | yes | ISO 4217, e.g. `NAD`, `ZAR` |
| `currency.symbol` | yes | how amounts are written, e.g. `N$` |
| `currency.minor_units` | yes | 0-3; money is always stored in minor units, this only says how to show it |
| `locales.default` | yes | a language code, e.g. `en` |
| `locales.supported` | yes | must include the default; the app ships `en` and `af` |
| `timezone` | yes | IANA zone; new merchants default to it, and opening hours are read in each merchant's zone |
| `map.centre`, `map.zoom` | yes | where maps open |
| `map.tiles` | yes | the tile provider: see [MAPS.md](MAPS.md) |
| `service_area` | no | circles (`name`, `lat`, `lng`, `radius_km`); a job with a stop outside all of them is refused. Leave out to work anywhere |
| `pack` | yes | the pack to run (`food`, `courier`, `freight`, or yours). `PACK` in the environment overrides it, for tests and demos |
| `roles` | no | which of the pack's roles you offer; without `customer` there is no public sign-up. Must include `admin`. Leave out for all |
| `payment_methods` | no | which of the pack's payment methods you offer. Leave out for all |
| `tenant_id` | no | stamped on accounts, merchants and jobs; default `default`. See [MULTI_TENANCY.md](MULTI_TENANCY.md) before changing it |
| `overrides.terminology` | no | per locale, words that replace the pack's (`agent: rider`) |
| `overrides.settings` | no | module settings that replace the pack's, a whole module block at a time; checked by the same rules as the pack's own |

## Languages

The app asks for `GET /config` in the device's language. The server
answers in it if `locales.supported` has it, otherwise in
`locales.default`, and says which. The app then shows its own words in
that same language, so the pack's nouns and the app's sentences never
mix two languages.

The app ships English and Afrikaans, and so do the shipped packs. To add
a language, for example `de`:

1. Add `apps/delivery_app/lib/l10n/app_de.arb` with every message in
   `app_en.arb`. `flutter test` fails if one is missing or drops a
   placeholder.
2. Add a `de:` block to your pack's `terminology` (nouns and state
   labels), or to `overrides.terminology` for the words you want to
   change. A word left out falls back to English.
3. Add `de` to `locales.supported`.

Some things stay in one language for now: `name` and `tagline` in this
file, and messages the server writes (API errors, notifications).

## How the app uses your colours

Only `colours.primary` is required. The app builds a Material 3 palette
from it and uses your other colours where you give them:

| You give | The app uses |
|---|---|
| `primary` | buttons, links, progress, the map marker. Words on it are black or white, whichever reads better |
| `secondary` | accents. Left out: derived from the primary |
| `background` | every screen's background. Left out: a near-white tint of the primary |
| `text` | body text. Left out: black or white, whichever reads better on the background |

Captions and hints sit between your text and background colours, so they
keep your hue and stay readable.

The server refuses a `text` colour that is hard to read on the
`background` (WCAG AA, contrast under 4.5:1) and names the ratio. The app
can't make a button unreadable, because it picks black or white for the
words on it. So a bright primary like `#E4572E` gets black button text:
white on it is only 3.6:1.

`font` must be a family the app bundles (add it under `flutter: fonts:`
in `apps/delivery_app/pubspec.yaml`). A name the app doesn't have falls
back to the platform's font. `GET /config` is cached for five minutes,
so after a restart it can take that long for open apps to pick up a new
brand.

A brand's overrides apply only to its own `pack`. If `PACK` in the
environment picks another pack, the server says so in its log and leaves
the overrides out.
