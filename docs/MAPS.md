# Map tiles

The app draws its maps (live tracking, agents on the move) from raster
tiles. Which tile server it uses is set in `config/brand.yaml`:

```yaml
map:
  centre: { lat: -22.5700, lng: 17.0836 }
  zoom: 13
  tiles:
    url: "https://api.maptiler.com/maps/streets-v2/256/{z}/{x}/{y}.png?key={key}"
    attribution: "© MapTiler © OpenStreetMap contributors"
    key_env: MAP_TILES_KEY
    max_zoom: 19
```

- `url` is a template with `{z}`, `{x}` and `{y}`. If the provider needs a
  key, put `{key}` where it goes and name the environment variable holding
  it in `key_env`; the server fills it in at boot and refuses to start if the
  variable is empty.
- `attribution` is required. Every provider's terms ask you to show it, and
  the app does, on every map.
- Tile keys are browser keys: anyone using the app can see them. Most
  providers let you restrict a key to your domains or apps; do that. Keep the
  key out of git anyway (set it in `.env`).

## Why not OpenStreetMap's own servers

`tile.openstreetmap.org` is run by volunteers for the OpenStreetMap project,
and its [tile usage policy](https://operations.osmfoundation.org/policies/tiles/)
doesn't allow using it as the tile source for an app like this. The shipped
`config/brand.yaml` uses it so the demo works out of the box, and the server
allows that only with `PROFILE=dev`. With any other profile it refuses to
boot until you pick a provider.

## Options

Check each provider's current terms, limits and prices before you choose;
they change.

| Option | What you get | Notes |
|---|---|---|
| A commercial tile service with a free tier (for example MapTiler, Stadia Maps, Thunderforest) | OpenStreetMap data, styled and hosted, behind a key | the quickest way to production; usage-based pricing above the free tier |
| Mapbox raster tiles | Mapbox's styles and data, behind an access token | its own terms on attribution and caching |
| Your own tile server (for example tileserver-gl with OpenMapTiles data, or a classic OSM rendering stack) | full control, no per-request cost | you run and update it; right for high volume or offline regions |

Whichever you pick, it's one edit to `map.tiles` and, for a keyed provider,
one environment variable. Nothing in the app or the server changes.
