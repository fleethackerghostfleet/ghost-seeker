//go:build prod

package web

import "embed"

// Production builds embed the console without static/imagery (demo frames).
const hasDemoImagery = false

//go:embed static/*.* static/brand static/fonts
var files embed.FS

const demoOceanArchive = ""
