# ext/: your modules

Your own backend modules live here, one folder each. Make one with:

```bash
go run ./cmd/platformctl new-module <name>
```

That writes `ext/<name>/` (with tests) and registers it in
`cmd/platform/extensions.go`. Its `README.md` says what each file is for.

An upgrade of the platform never changes anything in `ext/` except this
file (ADR-0004). The Dockerfile copies the folder into the image, so
the platform binary includes your modules.
