// Package migrations embeds this service's goose migration files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
