package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPreservesExistingKeyringEntries(t *testing.T) {
	dir := t.TempDir()
	if err := run("first", dir); err != nil {
		t.Fatal(err)
	}
	if err := run("second", dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]string
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys["first"] == "" || keys["second"] == "" {
		t.Fatalf("keyring=%v", keys)
	}
}

func TestRunSecuresExistingPrivateKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.key.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("existing", dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key mode=%#o, want 0600", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		KeyID      string `json:"key_id"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal(raw, &generated); err != nil {
		t.Fatal(err)
	}
	if generated.KeyID != "existing" || generated.PrivateKey == "" {
		t.Fatalf("private key content=%s, want generated key for existing", raw)
	}
}

func TestRunMalformedKeyringLeavesPrivateKeyUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.key.json")
	original := []byte("old private key")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keyring.json"), []byte(`{"broken":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("existing", dir); err == nil {
		t.Fatal("run succeeded with malformed keyring")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("private key changed: got %q, want %q", got, original)
	}
}
