package policy

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestSigningRoundTripTamperAndKeyErrors(t *testing.T) {
	pub, priv, err := GenerateKey("test")
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.Policy{Id: "p1", Version: 1, Text: "test", Interest: &pb.Interest{BoostLabels: []string{"submarine"}},
		Conflicts: []*pb.MergeConflict{{RuleId: "r", Reason: "reason"}}}
	if err := Sign(p, "test", priv); err != nil {
		t.Fatal(err)
	}
	if err := Verify(p, Keyring{"test": pub}); err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var binaryRoundTrip pb.Policy
	if err := proto.Unmarshal(raw, &binaryRoundTrip); err != nil {
		t.Fatal(err)
	}
	jsonRaw, err := protojson.Marshal(&binaryRoundTrip)
	if err != nil {
		t.Fatal(err)
	}
	var jsonRoundTrip pb.Policy
	if err := protojson.Unmarshal(jsonRaw, &jsonRoundTrip); err != nil {
		t.Fatal(err)
	}
	if err := Verify(&jsonRoundTrip, Keyring{"test": pub}); err != nil {
		t.Fatalf("re-marshaled signed policy does not verify: %v", err)
	}
	jsonRoundTrip.Interest.BoostLabels[0] = "aircraft"
	if err := Verify(&jsonRoundTrip, Keyring{"test": pub}); err != ErrBadSignature {
		t.Fatalf("tamper error=%v, want ErrBadSignature", err)
	}
	if err := Verify(p, Keyring{}); err == nil || !isError(err, ErrUnknownKey) {
		t.Fatalf("unknown key error=%v", err)
	}
	if err := Verify(&pb.Policy{}, Keyring{"test": pub}); err != ErrUnsigned {
		t.Fatalf("unsigned error=%v", err)
	}
}

func TestLoadSigningFiles(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := GenerateKey("operator")
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(dir, "keyring.json")
	if err := os.WriteFile(pubPath, []byte(`{"operator":"`+base64.StdEncoding.EncodeToString(pub)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadKeyring(pubPath)
	if err != nil || len(keys["operator"]) != ed25519.PublicKeySize {
		t.Fatalf("keyring=%v err=%v", keys, err)
	}
	privPath := filepath.Join(dir, "private.json")
	if err := os.WriteFile(privPath, []byte(`{"key_id":"operator","private_key":"`+base64.StdEncoding.EncodeToString(priv.Seed())+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := LoadSigningKey(privPath)
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.Policy{Id: "p1"}
	if err := key.Sign(p); err != nil || Verify(p, keys) != nil {
		t.Fatalf("loaded key failed to sign/verify: %v", err)
	}

	for name, content := range map[string]string{
		"bad-base64.json":   `{"operator":"$$$"}`,
		"wrong-length.json": `{"operator":"AQ=="}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKeyring(path); err == nil {
			t.Fatalf("LoadKeyring(%s) succeeded", name)
		}
	}
	for name, content := range map[string]string{
		"bad-private-base64.json":   `{"key_id":"x","private_key":"$$$"}`,
		"wrong-private-length.json": `{"key_id":"x","private_key":"AQ=="}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSigningKey(path); err == nil {
			t.Fatalf("LoadSigningKey(%s) succeeded", name)
		}
	}
}

func TestLoadKeyringRejectsDuplicatePublicKeys(t *testing.T) {
	pub, _, err := GenerateKey("duplicate")
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := os.WriteFile(path, []byte(`{"first":"`+encoded+`","second":"`+encoded+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyring(path); err == nil {
		t.Fatal("LoadKeyring accepted duplicate public keys")
	}
}

func isError(err, target error) bool {
	return err == target || (err != nil && target != nil && errorsIs(err, target))
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
