package console

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	"google.golang.org/protobuf/proto"
)

const operatorTestKey = "shore-console-test-key-at-least-32-bytes"

func makeOperatorService(t *testing.T, initial *pb.Policy) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if initial != nil {
		if err := st.SetPolicy(context.Background(), initial); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := unitauth.New(true, operatorTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{St: st, Clk: clock.Real{}, Operators: auth}, st
}

func operatorRequest(t *testing.T, policy *pb.Policy, role, id string, echelon pb.Echelon) *connect.Request[pb.SetPolicyRequest] {
	t.Helper()
	request := connect.NewRequest(&pb.SetPolicyRequest{Policy: policy})
	if role == "operator" {
		token, err := unitauth.MintOperator(id, echelon, operatorTestKey, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		request.Header().Set("Authorization", "Bearer "+token)
	} else if role == "unit" {
		token, err := unitauth.Mint(id, operatorTestKey, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestSetPolicyOperatorAuthentication(t *testing.T) {
	commandLayer := func() *pb.PolicyLayer {
		return &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
			Rules: []*pb.InterestRule{{Id: "locked", Locked: true, BoostLabels: []string{"submarine"}, Weight: 3}}}
	}
	shipLayer := func(issuer string) *pb.PolicyLayer {
		return &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: issuer,
			Rules: []*pb.InterestRule{{Id: "ship-rule", BoostLabels: []string{"tanker"}}}}
	}
	call := func(t *testing.T, svc *Service, req *connect.Request[pb.SetPolicyRequest], want connect.Code) *pb.Policy {
		t.Helper()
		response, err := svc.SetPolicy(context.Background(), req)
		if want != 0 {
			if connect.CodeOf(err) != want {
				t.Fatalf("SetPolicy error=%v, want %v", err, want)
			}
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg.Policy
	}
	stored := func(t *testing.T, st *store.Store) *pb.Policy {
		t.Helper()
		policy, err := st.GetPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return policy
	}

	t.Run("no token", func(t *testing.T) {
		svc, _ := makeOperatorService(t, nil)
		call(t, svc, connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Layers: []*pb.PolicyLayer{shipLayer("spoof")}}}),
			connect.CodeUnauthenticated)
	})

	t.Run("unit token", func(t *testing.T) {
		svc, _ := makeOperatorService(t, nil)
		call(t, svc, operatorRequest(t, &pb.Policy{Layers: []*pb.PolicyLayer{shipLayer("spoof")}},
			"unit", "edge-1", pb.Echelon_ECHELON_UNSPECIFIED), connect.CodeUnauthenticated)
	})

	t.Run("ship token preserves command layer and sets its own issuer", func(t *testing.T) {
		initial := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer()}}
		svc, st := makeOperatorService(t, initial)
		requestPolicy := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer(), shipLayer("spoof")}}
		got := call(t, svc, operatorRequest(t, requestPolicy, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP), 0)
		if len(got.Layers) != 2 || got.Layers[0].IssuedBy != "CTF-72" || got.Layers[1].IssuedBy != "TAO-DDG51" {
			t.Fatalf("stored layers=%+v", got.Layers)
		}
		if stored := stored(t, st); !proto.Equal(got, stored) {
			t.Fatalf("response policy differs from stored policy: got=%v stored=%v", got, stored)
		}
	})

	for _, tc := range []struct {
		name   string
		change func() []*pb.PolicyLayer
	}{
		{
			name: "adding a command rule",
			change: func() []*pb.PolicyLayer {
				layer := commandLayer()
				layer.Rules = append(layer.Rules, &pb.InterestRule{Id: "added", SuppressLabels: []string{"tanker"}})
				return []*pb.PolicyLayer{layer, shipLayer("TAO-DDG51")}
			},
		},
		{
			name: "changing a locked command rule",
			change: func() []*pb.PolicyLayer {
				layer := commandLayer()
				layer.Rules[0].BoostLabels = []string{"tanker"}
				return []*pb.PolicyLayer{layer, shipLayer("TAO-DDG51")}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer()}}
			svc, st := makeOperatorService(t, initial)
			before := stored(t, st)
			call(t, svc, operatorRequest(t, &pb.Policy{Layers: tc.change()}, "operator",
				"TAO-DDG51", pb.Echelon_ECHELON_SHIP), connect.CodePermissionDenied)
			if after := stored(t, st); !proto.Equal(before, after) {
				t.Fatalf("denied request changed policy: before=%v after=%v", before, after)
			}
		})
	}

	t.Run("dropping stored command layer", func(t *testing.T) {
		initial := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer(), shipLayer("TAO-DDG51")}}
		svc, st := makeOperatorService(t, initial)
		before := stored(t, st)
		call(t, svc, operatorRequest(t, &pb.Policy{Layers: []*pb.PolicyLayer{shipLayer("spoof")}},
			"operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP), connect.CodePermissionDenied)
		if after := stored(t, st); !proto.Equal(before, after) {
			t.Fatalf("denied request changed policy: before=%v after=%v", before, after)
		}
	})

	t.Run("command token edits command layer", func(t *testing.T) {
		initial := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer()}}
		svc, _ := makeOperatorService(t, initial)
		edited := commandLayer()
		edited.Rules[0].BoostLabels = []string{"tanker"}
		got := call(t, svc, operatorRequest(t, &pb.Policy{Layers: []*pb.PolicyLayer{edited}},
			"operator", "CTF-72", pb.Echelon_ECHELON_COMMAND), 0)
		if len(got.Layers) != 1 || got.Layers[0].IssuedBy != "CTF-72" || got.Layers[0].Rules[0].BoostLabels[0] != "tanker" {
			t.Fatalf("command operator result=%+v", got.Layers)
		}
	})

	t.Run("requires layered policy", func(t *testing.T) {
		svc, _ := makeOperatorService(t, nil)
		call(t, svc, operatorRequest(t, &pb.Policy{}, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP),
			connect.CodeFailedPrecondition)
	})
}
