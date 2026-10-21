// Package web embeds the admin UI's HTML templates and static assets.
package web

import "embed"

// Files holds templates/*.html and static/*.
//
//go:embed templates static
var Files embed.FS
