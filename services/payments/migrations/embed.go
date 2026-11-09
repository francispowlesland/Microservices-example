// Package migrations embeds the goose SQL migrations for the payments database.
package migrations

import "embed"

// FS holds the *.sql migration files at its root.
//
//go:embed *.sql
var FS embed.FS
