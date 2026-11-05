package policy

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

var (
	ErrUnsigned     = errors.New("unsigned policy")
	ErrUnknownKey   = errors.New("unknown policy signing key")
	ErrBadSignature = errors.New("invalid policy signature")
)

type Keyring map[string]ed25519.PublicKey

func SigningBytes(p *pb.Policy) ([]byte, error) {
	if p == nil {
		return nil, errors.New("policy required")
	}
	clone := proto.Clone(p).(*pb.Policy)
	clone.Signature = nil
	return (proto.MarshalOptions{Deterministic: true}).Marshal(clone)
}

func Sign(p *pb.Policy, keyID string, priv ed25519.PrivateKey) error {
	if p == nil {
		return errors.New("policy required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("private key must be %d bytes", ed25519.PrivateKeySize)
	}
	raw, err := SigningBytes(p)
	if err != nil {
		return err
	}
	p.Signature = &pb.PolicySignature{KeyId: keyID, Ed25519: ed25519.Sign(priv, raw)}
	return nil
}

func Verify(p *pb.Policy, keys Keyring) error {
	if p == nil || p.Signature == nil || len(p.Signature.Ed25519) == 0 {
		return ErrUnsigned
	}
	pub, ok := keys[p.Signature.KeyId]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, p.Signature.KeyId)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: key %s has invalid length", ErrUnknownKey, p.Signature.KeyId)
	}
	raw, err := SigningBytes(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, raw, p.Signature.Ed25519) {
		return ErrBadSignature
	}
	return nil
}

func GenerateKey(keyID string) (pub ed25519.PublicKey, priv ed25519.PrivateKey, err error) {
	pub, priv, err = ed25519.GenerateKey(rand.Reader)
	return pub, priv, err
}

func LoadKeyring(path string) (Keyring, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	encoded := map[string]string{}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode keyring: %w", err)
	}
	keys := make(Keyring, len(encoded))
	for id, value := range encoded {
		key, err := decodeBase64(value)
		if err != nil {
			return nil, fmt.Errorf("decode key %q: %w", id, err)
		}
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %q must be %d bytes", id, ed25519.PublicKeySize)
		}
		keys[id] = ed25519.PublicKey(key)
	}
	return keys, nil
}

type SigningKey struct {
	KeyID   string
	Private ed25519.PrivateKey
}

func LoadSigningKey(path string) (*SigningKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var encoded struct {
		KeyID      string `json:"key_id"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode signing key: %w", err)
	}
	key, err := decodeBase64(encoded.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	switch len(key) {
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(key)
	case ed25519.PrivateKeySize:
	default:
		return nil, fmt.Errorf("private key must be %d-byte seed or %d-byte key", ed25519.SeedSize, ed25519.PrivateKeySize)
	}
	if encoded.KeyID == "" {
		return nil, errors.New("key_id required")
	}
	return &SigningKey{KeyID: encoded.KeyID, Private: ed25519.PrivateKey(key)}, nil
}

func (k *SigningKey) Sign(p *pb.Policy) error {
	if k == nil {
		return errors.New("signing key required")
	}
	return Sign(p, k.KeyID, k.Private)
}

func decodeBase64(value string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}
