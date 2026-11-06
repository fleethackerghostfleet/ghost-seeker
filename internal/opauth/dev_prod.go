//go:build prod

package opauth

import (
	"errors"
	"flag"
)

// DevTokenFileFlag is a no-op in production builds: the dev token file is
// compiled out, so an explicit operator token (or PIV) is required.
func DevTokenFileFlag(*flag.FlagSet) *string {
	none := "none"
	return &none
}

func loadDevToken(string) (string, error) {
	return "", errors.New("production build: operator token required (set -operator-token-file / GS_OPERATOR_TOKEN_FILE or GS_OPERATOR_TOKEN)")
}
