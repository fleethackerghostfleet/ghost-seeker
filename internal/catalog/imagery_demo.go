//go:build !prod

package catalog

import _ "embed"

// imageryJSON is the simulation's demo image library (web/static/imagery).
//
//go:embed imagery.json
var imageryJSON []byte
