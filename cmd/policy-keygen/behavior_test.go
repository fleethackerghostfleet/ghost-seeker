package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

func TestRunRejectsUnsafeIDsWithoutCreatingOutput(t *testing.T) {
	for _, id := range []string{"", "../escape", "nested/id", `nested\id`, "/absolute"} {
		t.Run(id, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "not-created")
			if err := run(id, out); err == nil || !strings.Contains(err.Error(), "filename-safe") {
				t.Fatalf("unsafe id accepted: %v", err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("invalid id created output directory: %v", err)
			}
		})
	}
}

func TestMainCreatesUsableKeyFromNullKeyring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keyring.json"), []byte("null"), 0644); err != nil {
		t.Fatal(err)
	}
	oldFlags, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldFlags, oldArgs })
	flag.CommandLine = flag.NewFlagSet("policy-keygen", flag.ExitOnError)
	os.Args = []string{"policy-keygen", "-id=operator-2026", "-out-dir=" + dir}
	main()
	keys, err := policy.LoadKeyring(filepath.Join(dir, "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(keys["operator-2026"]) != ed25519.PublicKeySize {
		t.Fatal("public key missing from keyring")
	}
	privatePath := filepath.Join(dir, "operator-2026.key.json")
	raw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		KeyID string `json:"key_id"`
		Seed  string `json:"private_key"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal("invalid private key JSON")
	}
	seed, err := base64.StdEncoding.DecodeString(record.Seed)
	if err != nil || len(seed) != ed25519.SeedSize || record.KeyID != "operator-2026" {
		t.Fatal("invalid private key encoding or identity")
	}
	message := []byte("policy signing round trip")
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), message)
	if !ed25519.Verify(keys[record.KeyID], message, signature) {
		t.Fatal("private/public keys do not match")
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("private JSON lacks terminal newline")
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, privatePath: 0600, filepath.Join(dir, "keyring.json"): 0644} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("mode for %s = %o; want %o", filepath.Base(path), info.Mode().Perm(), mode)
		}
	}
	assertNoPrivateTemps(t, dir)
}

func TestRunFilesystemErrorsDoNotPublishPrivateKeys(t *testing.T) {
	for _, name := range []string{"output-is-file", "keyring-is-directory", "private-is-directory"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "keys")
			if name == "output-is-file" {
				if err := os.WriteFile(out, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(out, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(out, "keyring.json")
				if name == "private-is-directory" {
					path = filepath.Join(out, "new.key.json")
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := run("new", out); err == nil {
				t.Fatal("filesystem error not returned")
			}
			if name == "output-is-file" {
				data, err := os.ReadFile(out)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("existing output modified: %v", err)
				}
			} else {
				assertNoPrivateTemps(t, out)
				if name == "keyring-is-directory" {
					if _, err := os.Stat(filepath.Join(out, "new.key.json")); !os.IsNotExist(err) {
						t.Fatalf("private key created despite unreadable keyring: %v", err)
					}
				} else if _, err := os.Stat(filepath.Join(out, "keyring.json")); !os.IsNotExist(err) {
					t.Fatalf("keyring published despite failed private key write: %v", err)
				}
			}
		})
	}
}

func assertNoPrivateTemps(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".private-key-*.tmp"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("temporary key files remain: %v, %v", paths, err)
	}
}

func TestAtomicPrivateWriteReplacesSymlinkWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	target, path := filepath.Join(dir, "target"), filepath.Join(dir, "key.json")
	if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateKeyAtomic(dir, path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatalf("symlink target modified: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement not published: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("replacement is not a private regular file")
	}
	assertNoPrivateTemps(t, dir)
}

func TestAtomicPrivateWriteErrorsCleanUp(t *testing.T) {
	for _, name := range []string{"missing-temp-directory", "missing-destination-parent", "destination-is-directory"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path, tempDir := filepath.Join(dir, "key.json"), dir
			switch name {
			case "missing-temp-directory":
				tempDir = filepath.Join(dir, "missing")
			case "missing-destination-parent":
				path = filepath.Join(dir, "missing", "key.json")
			case "destination-is-directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := writePrivateKeyAtomic(tempDir, path, []byte("not-a-real-key")); err == nil {
				t.Fatal("atomic write unexpectedly succeeded")
			}
			assertNoPrivateTemps(t, dir)
			if name == "destination-is-directory" {
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != 0 {
					t.Fatalf("destination directory changed: %v", err)
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("failed write published destination: %v", err)
			}
		})
	}
}

func TestPolicyKeygenCommandProcess(t *testing.T) {
	if os.Getenv("GHOST_KEYGEN_TEST_PROCESS") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("policy-keygen", flag.ExitOnError)
	os.Args = []string{"policy-keygen", "-id="}
	main()
}

func TestMainRejectsMissingID(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestPolicyKeygenCommandProcess$")
	cmd.Env = append(os.Environ(), "GHOST_KEYGEN_TEST_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "-id must be a non-empty filename-safe key id") {
		t.Fatalf("missing-id exit = %v; stderr = %q", err, stderr.String())
	}
}
