// Package migrations embeds the users service goose migrations.
package migrations

import "embed"

// FS holds the *.sql migration files at its root.
//
//go:embed *.sql
var FS embed.FS
