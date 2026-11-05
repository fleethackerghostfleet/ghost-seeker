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
	if err := run(*id, *outDir); err != nil {
		fatal(err)
	}
}

func run(id, outDir string) error {
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("-id must be a non-empty filename-safe key id")
	}
	pub, priv, err := policy.GenerateKey(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return err
	}
	privateJSON, err := json.MarshalIndent(map[string]string{
		"key_id": id, "private_key": base64.StdEncoding.EncodeToString(priv.Seed()),
	}, "", "  ")
	if err != nil {
		return err
	}
	privatePath := filepath.Join(outDir, id+".key.json")
	if err := os.WriteFile(privatePath, append(privateJSON, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(privatePath, 0o600); err != nil {
		return err
	}
	keyringPath := filepath.Join(outDir, "keyring.json")
	keyring := make(map[string]string)
	if raw, err := os.ReadFile(keyringPath); err == nil {
		if err := json.Unmarshal(raw, &keyring); err != nil {
			return err
		}
		if keyring == nil {
			keyring = make(map[string]string)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	keyring[id] = base64.StdEncoding.EncodeToString(pub)
	keyringJSON, err := json.MarshalIndent(keyring, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyringPath, append(keyringJSON, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
