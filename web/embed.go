// Package web embeds the operator UI.
package web

import "embed"

//go:embed index.html
var FS embed.FS
