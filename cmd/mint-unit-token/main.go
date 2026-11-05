// mint-unit-token writes an HS256 unit or operator token to stdout; redirect it to a protected file.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mint-unit-token", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("id", os.Getenv("EDGE_ID"), "edge node id, or operator id for -role operator")
	role := flags.String("role", "unit", "token role: unit or operator")
	echelon := flags.String("echelon", "", "operator echelon: unit, ship, or command (required for -role operator)")
	key := flags.String("signing-key", os.Getenv("GS_SIGNING_KEY"), "HS256 signing key (prefer env or file)")
	file := flags.String("signing-key-file", os.Getenv("GS_SIGNING_KEY_FILE"), "signing key file")
	ttl := flags.Duration("ttl", time.Hour, "token lifetime (reference tooling: one hour)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *role != "unit" && *role != "operator" {
		fmt.Fprintf(stderr, "invalid -role %q: must be unit or operator\n", *role)
		return 1
	}
	if *echelon != "" {
		if _, err := unitauth.EchelonFromString(*echelon); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	secret, err := unitauth.Credential(*key, *file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var token string
	if *role == "operator" {
		if *echelon == "" {
			fmt.Fprintln(stderr, "-echelon is required for -role operator")
			return 1
		}
		level, err := unitauth.EchelonFromString(*echelon)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		token, err = unitauth.MintOperator(*id, level, secret, *ttl)
	} else {
		token, err = unitauth.Mint(*id, secret, *ttl)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, token); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
