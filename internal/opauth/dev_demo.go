//go:build !prod

package opauth

import (
	"flag"
	"os"
)

// DevTokenFileFlag registers -dev-token-file on fs. Production builds do not
// register it and always require an explicit operator token (or PIV).
func DevTokenFileFlag(fs *flag.FlagSet) *string {
	def := DefaultDevFile
	if v := os.Getenv("GS_DEV_TOKEN_FILE"); v != "" {
		def = v
	}
	return fs.String("dev-token-file", def, "local demo only: auto-generate the operator token here when none is set ('none' = require an explicit token)")
}

func loadDevToken(path string) (string, error) { return loadOrCreate(path) }
