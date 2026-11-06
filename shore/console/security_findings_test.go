package console

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestShoreSignerCannotRestoreCommandLayer(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pub, priv, err := policy.GenerateKey("shore")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	svc := &Service{St: st, Clk: clock.NewVirtual(now), Signer: &policy.SigningKey{KeyID: "shore", Private: priv}, Keys: policy.Keyring{"shore": pub}}
	replay := &pb.Policy{Id: "revoked", Version: now.UnixMilli() + 100, IssuedAtUnixMs: now.UnixMilli(), Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "command", Emcon: &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT}}}}
	for _, keyID := range []string{"shore", "alias"} {
		svc.Keys = policy.Keyring{keyID: pub}
		if err := policy.Sign(replay, keyID, priv); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: replay})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("shore replay accepted with key %s: %v", keyID, err)
		}
	}
}

func TestCommandVersionSurvivesShoreEnvelopeVersion(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pub, priv, err := policy.GenerateKey("command")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	svc := &Service{St: st, Clk: clock.NewVirtual(now), Keys: policy.Keyring{"command": pub}}
	p := &pb.Policy{Id: "command", Version: 17, IssuedAtUnixMs: now.UnixMilli(), Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "command", Rules: []*pb.InterestRule{{Id: "boost", BoostLabels: []string{"vessel"}}}}}}
	if err := policy.Sign(p, "command", priv); err != nil {
		t.Fatal(err)
	}
	result, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: p}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Msg.Policy.Layers[0].CommandVersion != 17 || result.Msg.Policy.Version == 17 {
		t.Fatalf("command version lost: %v", result.Msg.Policy)
	}
	next := proto.Clone(result.Msg.Policy).(*pb.Policy)
	next.Version = 18
	next.Layers[0].Rules[0].Id = "changed"
	if err := policy.Sign(next, "command", priv); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: next})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("stale command-layer sequence accepted: %v", err)
	}
}
