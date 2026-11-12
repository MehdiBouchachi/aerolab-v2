// Package migrations embeds the ordered SQL migration files.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
