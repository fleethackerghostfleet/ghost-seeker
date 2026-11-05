package console

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

func TestSetPolicyFlattensVersionsAndSigns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	pub, priv, err := policy.GenerateKey("shore")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{NodeID: "shore", St: st, Clk: clk, Embedder: inference.NewMock(),
		Signer: &policy.SigningKey{KeyID: "shore", Private: priv}, EdgeID: "edge-1"}
	layered := &pb.Policy{Id: "layered", IssuedBy: "TAO", Layers: []*pb.PolicyLayer{
		{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72", Rules: []*pb.InterestRule{
			{Id: "locked", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
		}},
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Rules: []*pb.InterestRule{
			{Id: "blocked", SuppressLabels: []string{"submarine"}},
		}},
	}}
	response, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: layered}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.Policy.Interest == nil || len(response.Msg.Policy.Interest.BoostLabels) != 1 ||
		len(response.Msg.Conflicts) != 1 || response.Msg.Conflicts[0].RuleId != "blocked" {
		t.Fatalf("response=%+v", response.Msg)
	}
	if err := policy.Verify(response.Msg.Policy, policy.Keyring{"shore": pub}); err != nil {
		t.Fatalf("signature failed verification: %v", err)
	}
	firstVersion := response.Msg.Policy.Version
	if firstVersion == 0 {
		t.Fatalf("version not assigned: %d", firstVersion)
	}
	next, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "next"}}))
	if err != nil {
		t.Fatal(err)
	}
	if next.Msg.Policy.Version != firstVersion+1 {
		t.Fatalf("next version=%d, want %d", next.Msg.Policy.Version, firstVersion+1)
	}
	if err := policy.Verify(next.Msg.Policy, policy.Keyring{"shore": pub}); err != nil {
		t.Fatalf("second signature failed verification: %v", err)
	}
}
