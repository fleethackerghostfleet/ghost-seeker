// Package apidocs embeds the production API's versioned contract.
package apidocs

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
