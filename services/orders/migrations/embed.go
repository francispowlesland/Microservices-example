// Package migrations embeds the goose SQL migrations for the orders database.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
