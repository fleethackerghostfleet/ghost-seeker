package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

func main() {
	id := flag.String("id", "", "key id")
	outDir := flag.String("out-dir", ".", "directory for generated key files")
	flag.Parse()
	if *id == "" || filepath.Base(*id) != *id || strings.ContainsAny(*id, `/\\`) {
		fatal(fmt.Errorf("-id must be a non-empty filename-safe key id"))
	}
	pub, priv, err := policy.GenerateKey(*id)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fatal(err)
	}
	privateJSON, err := json.MarshalIndent(map[string]string{
		"key_id": *id, "private_key": base64.StdEncoding.EncodeToString(priv.Seed()),
	}, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, *id+".key.json"), append(privateJSON, '\n'), 0o600); err != nil {
		fatal(err)
	}
	keyringJSON, err := json.MarshalIndent(map[string]string{
		*id: base64.StdEncoding.EncodeToString(pub),
	}, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "keyring.json"), append(keyringJSON, '\n'), 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
