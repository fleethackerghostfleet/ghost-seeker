// mint-unit-token writes an HS256 unit token to stdout; redirect it to a protected file.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

func main() {
	node := flag.String("id", os.Getenv("EDGE_ID"), "edge node id (JWT sub)")
	key := flag.String("signing-key", os.Getenv("GS_SIGNING_KEY"), "HS256 signing key (prefer env or file)")
	file := flag.String("signing-key-file", os.Getenv("GS_SIGNING_KEY_FILE"), "signing key file")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime (reference tooling: one hour)")
	flag.Parse()
	secret, err := unitauth.Credential(*key, *file)
	if err == nil {
		var token string
		token, err = unitauth.Mint(*node, secret, *ttl)
		if err == nil {
			fmt.Println(token)
			return
		}
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
