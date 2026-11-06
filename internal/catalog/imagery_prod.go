//go:build prod

package catalog

// Production builds carry no demo image library.
var imageryJSON = []byte("[]")
