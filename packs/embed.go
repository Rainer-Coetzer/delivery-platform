// Package packfs embeds every vertical pack in this directory into the
// binary, so a single `platform` binary carries its packs with it. A buyer's
// own pack (packs/<theirs>/) is picked up here at build time too.
package packfs

import "embed"

// FS holds packs/<name>/pack.yaml and lifecycle.yaml for every pack.
//
//go:embed */pack.yaml */lifecycle.yaml
var FS embed.FS
