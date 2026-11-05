package policy

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"google.golang.org/protobuf/proto"
)

func TestStoreLastGoodTTLAndRestart(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.json"
	backend, err := NewFileBackend(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	store, err := NewStore(ctx, backend, Options{TTL: 100 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	input := &pb.Policy{Id: "good", Version: 5, IssuedAtUnixMs: now.UnixMilli(), Text: "last good"}
	got, err := store.Accept(ctx, input, "console", now)
	if err != nil || !proto.Equal(got, input) {
		t.Fatalf("accept=%v err=%v", got, err)
	}
	reopened, err := NewFileBackend(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewStore(ctx, reopened, Options{TTL: 100 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	active, expired := restored.Active(now.Add(50 * time.Second))
	if expired || !proto.Equal(active, input) {
		t.Fatalf("restored active=%v expired=%v", active, expired)
	}
	active, expired = restored.Active(now.Add(100 * time.Second))
	if !expired || active != nil {
		t.Fatalf("expired active=%v expired=%v", active, expired)
	}
	if err := restored.MarkExpired(ctx, now.Add(100*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restored.MarkExpired(ctx, now.Add(101*time.Second)); err != nil {
		t.Fatal(err)
	}
	audit, err := restored.Audit(ctx, 0)
	if err != nil || len(audit) != 2 || audit[0].Result != "expired" || audit[1].Result != "applied" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestStoreVersionHighWater(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	backend, err := NewFileBackend(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, backend, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version int64
		wantErr error
	}{
		{version: 0},
		{version: 5},
		{version: 5, wantErr: ErrStaleVersion},
		{version: 4, wantErr: ErrStaleVersion},
		{version: 6},
	} {
		p := &pb.Policy{Id: "p", Version: tc.version, IssuedAtUnixMs: now.UnixMilli()}
		_, err := store.Accept(ctx, p, "console", now)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("version %d error=%v, want %v", tc.version, err, tc.wantErr)
		}
	}
	reloaded, err := NewStore(ctx, backend, Options{})
	if err != nil || reloaded.HighWater() != 6 {
		t.Fatalf("highwater=%d err=%v", reloaded.HighWater(), err)
	}
	if _, err := reloaded.Accept(ctx, &pb.Policy{Id: "next", Version: 5, IssuedAtUnixMs: now.UnixMilli()}, "console", now); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("version 5 after restart error=%v", err)
	}
}

func TestStoreExplicitExpiryAndSignedRequirements(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mem := NewMemBackend()
	pub, priv, err := GenerateKey("k")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(ctx, mem, Options{Keys: Keyring{"k": pub}, RequireSigned: true})
	if err != nil {
		t.Fatal(err)
	}
	previous := &pb.Policy{Id: "previous", IssuedAtUnixMs: now.UnixMilli()}
	if err := Sign(previous, "k", priv); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(ctx, previous, "console", now); err != nil {
		t.Fatal(err)
	}
	unsigned := &pb.Policy{Id: "unsigned", IssuedAtUnixMs: now.UnixMilli()}
	if _, err := store.Accept(ctx, unsigned, "downlink", now); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned downlink error=%v", err)
	}
	if active, _ := store.Active(now); active.Id != "previous" {
		t.Fatalf("rejected policy replaced last good: %v", active)
	}
	if _, err := store.Accept(ctx, unsigned, "console", now); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned console error=%v", err)
	}
	if active, _ := store.Active(now); active == nil || active.Id != "previous" {
		t.Fatalf("console unsigned policy replaced last good: %v", active)
	}
	expired := &pb.Policy{Id: "explicit", IssuedAtUnixMs: now.UnixMilli(), ExpiresAtUnixMs: now.UnixMilli()}
	if err := Sign(expired, "k", priv); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(ctx, expired, "console", now); !errors.Is(err, ErrExpired) {
		t.Fatalf("explicit expiry error=%v", err)
	}
	badSignature := &pb.Policy{Id: "signed", IssuedAtUnixMs: now.UnixMilli()}
	badSignature.Signature = &pb.PolicySignature{KeyId: "k", Ed25519: make([]byte, ed25519.SignatureSize)}
	if _, err := store.Accept(ctx, badSignature, "console", now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("bad signature error=%v", err)
	}
	_ = priv
}

func TestStoreAuditAppliedRejectedExpiredNewestFirst(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store, err := NewStore(ctx, NewMemBackend(), Options{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(ctx, &pb.Policy{Id: "p", Version: 2, IssuedAtUnixMs: now.UnixMilli()}, "ui", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(ctx, &pb.Policy{Id: "stale", Version: 2, IssuedAtUnixMs: now.UnixMilli()}, "admin", now); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale error=%v", err)
	}
	store.Reject(ctx, &pb.Policy{Id: "rejected", IssuedBy: "TAO", Version: 3}, "ui", "rule conflict", now.Add(time.Second))
	if err := store.MarkExpired(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkExpired(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	audit, err := store.Audit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 4 || audit[0].Result != "expired" || audit[1].Result != "rejected" ||
		audit[2].Result != "rejected" || audit[3].Result != "applied" {
		t.Fatalf("audit order=%+v", audit)
	}
	if audit[1].Reason != "rule conflict" || audit[2].Reason != ErrStaleVersion.Error() {
		t.Fatalf("audit reasons=%+v", audit)
	}
}
