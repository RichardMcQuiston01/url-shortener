// Package migrations embeds the ordered SQL migration files (0001_init.sql,
// ...) that the store applies at startup.
package migrations

import "embed"

// Files holds every migration, named NNNN_description.sql. Files are applied
// in lexical order, so keep the numeric prefix zero-padded.
//
//go:embed *.sql
var Files embed.FS
