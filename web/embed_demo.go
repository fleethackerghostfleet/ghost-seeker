//go:build !prod

package web

import "embed"

const hasDemoImagery = true

//go:embed static
var files embed.FS
