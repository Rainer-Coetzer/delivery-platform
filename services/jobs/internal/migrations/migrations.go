// Package migrations embeds this service's goose migration files so the
// binary can migrate its own schema on startup with no separate step.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
