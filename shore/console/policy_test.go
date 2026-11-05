package console

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/proto"
	"sync"
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
	cmdPub, cmdPriv, err := policy.GenerateKey("ctf72")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{NodeID: "shore", St: st, Clk: clk, Embedder: inference.NewMock(),
		Signer: &policy.SigningKey{KeyID: "shore", Private: priv}, Keys: policy.Keyring{"ctf72": cmdPub}, EdgeID: "edge-1"}
	command := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72", Rules: []*pb.InterestRule{
		{Id: "locked", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3},
	}}
	layered := &pb.Policy{Id: "layered", IssuedBy: "TAO", Version: 10, Layers: []*pb.PolicyLayer{
		command,
		{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO", Rules: []*pb.InterestRule{
			{Id: "blocked", SuppressLabels: []string{"submarine"}},
		}},
	}}
	// A new COMMAND layer the shore has never held is refused unless the
	// submitted policy is signed by a trusted higher-echelon key.
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: proto.Clone(layered).(*pb.Policy)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unsigned command layer accepted: %v", err)
	}
	if err := policy.Sign(layered, "ctf72", cmdPriv); err != nil {
		t.Fatal(err)
	}
	signedCommand := proto.Clone(layered).(*pb.Policy)
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
	// Ship-level edits that keep the held COMMAND layer unchanged need no signature.
	next, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "next",
		Layers: []*pb.PolicyLayer{proto.Clone(command).(*pb.PolicyLayer), {Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO"}}}}))
	if err != nil {
		t.Fatal(err)
	}
	// Dropping or altering the held COMMAND layer is refused.
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "drop"}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("dropping the command layer accepted: %v", err)
	}
	// Command revokes its layer with a newer signed policy (one above what
	// GetPolicy shows after the ship edit)...
	revoke := &pb.Policy{Id: "revoke", IssuedBy: "CTF-72", Version: next.Msg.Policy.Version + 1,
		Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "TAO"}}}
	if err := policy.Sign(revoke, "ctf72", cmdPriv); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: revoke})); err != nil {
		t.Fatalf("signed revoke refused: %v", err)
	}
	// ...and replaying the old signed command policy (lower version) is refused,
	// so revoked command rules cannot be restored by re-submitting them.
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: proto.Clone(signedCommand).(*pb.Policy)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("replayed signed command policy accepted: %v", err)
	}
	if next.Msg.Policy.Version <= firstVersion || next.Msg.Policy.Version < clk.Now().UnixMilli() {
		t.Fatalf("next version=%d, want > %d and >= clock ms", next.Msg.Policy.Version, firstVersion)
	}
	if err := policy.Verify(next.Msg.Policy, policy.Keyring{"shore": pub}); err != nil {
		t.Fatalf("second signature failed verification: %v", err)
	}
}

func TestSetPolicyConcurrentVersionsAreDistinct(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := &Service{St: st, Clk: clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
	const count = 20
	versions := make(chan int64, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			response, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{
				Policy: &pb.Policy{Id: fmt.Sprintf("policy-%d", i)},
			}))
			if err != nil {
				errs <- err
				return
			}
			versions <- response.Msg.Policy.Version
		}(i)
	}
	wg.Wait()
	close(versions)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[int64]bool, count)
	for version := range versions {
		if seen[version] {
			t.Fatalf("duplicate policy version %d", version)
		}
		seen[version] = true
	}
	if len(seen) != count {
		t.Fatalf("got %d unique versions, want %d", len(seen), count)
	}
}
