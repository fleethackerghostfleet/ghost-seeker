//go:build prod

// Package buildprofile reports which build profile a binary was compiled
// with. Production builds use `-tags prod`, which compiles demo features out.
package buildprofile

// Prod is true only in `-tags prod` builds.
const Prod = true

// Name is "demo" or "prod".
const Name = "prod"
