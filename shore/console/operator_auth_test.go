package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
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
		request.Header().Set(unitauth.OperatorJWTHeader, token)
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

	t.Run("ship token gets the layer authorization error before signature validation", func(t *testing.T) {
		initial := &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer()}}
		svc, _ := makeOperatorService(t, initial)
		edited := commandLayer()
		edited.Rules[0].BoostLabels = []string{"tanker"}
		_, err := svc.SetPolicy(context.Background(), operatorRequest(t, &pb.Policy{
			Layers: []*pb.PolicyLayer{edited, shipLayer("TAO-DDG51")},
		}, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP))
		if connect.CodeOf(err) != connect.CodePermissionDenied ||
			!strings.Contains(err.Error(), "cannot change ECHELON_COMMAND layer with ECHELON_SHIP token") {
			t.Fatalf("SetPolicy error=%v, want ship-token command-layer denial", err)
		}
	})

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

	t.Run("command token edits command layer with trusted signature", func(t *testing.T) {
		initial := &pb.Policy{Version: 1, Layers: []*pb.PolicyLayer{commandLayer()}}
		svc, _ := makeOperatorService(t, initial)
		public, private, err := policy.GenerateKey("ctf72")
		if err != nil {
			t.Fatal(err)
		}
		svc.Keys = policy.Keyring{"ctf72": public}
		edited := commandLayer()
		edited.Rules[0].BoostLabels = []string{"tanker"}
		submitted := &pb.Policy{Version: 2, Layers: []*pb.PolicyLayer{edited}}
		if err := policy.Sign(submitted, "ctf72", private); err != nil {
			t.Fatal(err)
		}
		got := call(t, svc, operatorRequest(t, submitted,
			"operator", "CTF-72", pb.Echelon_ECHELON_COMMAND), 0)
		if len(got.Layers) != 1 || got.Layers[0].IssuedBy != "CTF-72" || got.Layers[0].Rules[0].BoostLabels[0] != "tanker" {
			t.Fatalf("command operator result=%+v", got.Layers)
		}
	})

	t.Run("unsigned command token edit is denied", func(t *testing.T) {
		initial := &pb.Policy{Version: 1, Layers: []*pb.PolicyLayer{commandLayer()}}
		svc, _ := makeOperatorService(t, initial)
		public, _, err := policy.GenerateKey("ctf72")
		if err != nil {
			t.Fatal(err)
		}
		svc.Keys = policy.Keyring{"ctf72": public}
		edited := commandLayer()
		edited.Rules[0].BoostLabels = []string{"tanker"}
		call(t, svc, operatorRequest(t, &pb.Policy{Version: 2, Layers: []*pb.PolicyLayer{edited}},
			"operator", "CTF-72", pb.Echelon_ECHELON_COMMAND), connect.CodePermissionDenied)
	})

	t.Run("requires layered policy", func(t *testing.T) {
		svc, _ := makeOperatorService(t, nil)
		call(t, svc, operatorRequest(t, &pb.Policy{}, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP),
			connect.CodeFailedPrecondition)
	})
}

func TestShipOperatorCannotShortenPolicyExpiry(t *testing.T) {
	commandLayer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "command", Locked: true, BoostLabels: []string{"submarine"}}}}
	previousExpiry := time.Now().Add(time.Hour).UnixMilli()
	svc, st := makeOperatorService(t, &pb.Policy{
		ExpiresAtUnixMs: previousExpiry,
		Layers:          []*pb.PolicyLayer{commandLayer},
	})
	requestPolicy := &pb.Policy{
		ExpiresAtUnixMs: time.Now().Add(time.Minute).UnixMilli(),
		Layers: []*pb.PolicyLayer{
			proto.Clone(commandLayer).(*pb.PolicyLayer),
			{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "spoof",
				Rules: []*pb.InterestRule{{Id: "ship", BoostLabels: []string{"tanker"}}}},
		},
	}
	request := operatorRequest(t, requestPolicy, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP)
	response, err := svc.SetPolicy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Msg.Policy.GetExpiresAtUnixMs(); got != previousExpiry {
		t.Fatalf("response expiry=%d, want prior expiry %d", got, previousExpiry)
	}
	stored, err := st.GetPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetExpiresAtUnixMs(); got != previousExpiry {
		t.Fatalf("stored expiry=%d, want prior expiry %d", got, previousExpiry)
	}
}

func TestShipOperatorKeepsRequestedExpiryWhenPreviousExpiryExpired(t *testing.T) {
	commandLayer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "command", Locked: true, BoostLabels: []string{"submarine"}}}}
	svc, st := makeOperatorService(t, &pb.Policy{
		ExpiresAtUnixMs: time.Now().Add(-time.Hour).UnixMilli(),
		Layers:          []*pb.PolicyLayer{commandLayer},
	})
	requestedExpiry := time.Now().Add(time.Hour).UnixMilli()
	request := operatorRequest(t, &pb.Policy{
		ExpiresAtUnixMs: requestedExpiry,
		Layers: []*pb.PolicyLayer{
			proto.Clone(commandLayer).(*pb.PolicyLayer),
			{Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "spoof",
				Rules: []*pb.InterestRule{{Id: "ship", BoostLabels: []string{"tanker"}}}},
		},
	}, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP)
	if _, err := svc.SetPolicy(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetExpiresAtUnixMs(); got != requestedExpiry {
		t.Fatalf("stored expiry=%d, want requested expiry %d", got, requestedExpiry)
	}
}

func TestShipOperatorKeepsRequestedExpiryOnFirstPolicy(t *testing.T) {
	svc, st := makeOperatorService(t, nil)
	requestedExpiry := time.Now().Add(time.Hour).UnixMilli()
	request := operatorRequest(t, &pb.Policy{
		ExpiresAtUnixMs: requestedExpiry,
		Layers: []*pb.PolicyLayer{{
			Echelon: pb.Echelon_ECHELON_SHIP, IssuedBy: "spoof",
			Rules: []*pb.InterestRule{{Id: "ship", BoostLabels: []string{"tanker"}}},
		}},
	}, "operator", "TAO-DDG51", pb.Echelon_ECHELON_SHIP)
	if _, err := svc.SetPolicy(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.GetExpiresAtUnixMs(); got != requestedExpiry {
		t.Fatalf("stored expiry=%d, want requested expiry %d", got, requestedExpiry)
	}
}

func TestOperatorAttributionAndEMCONUseCallerEchelon(t *testing.T) {
	commandLayer := &pb.PolicyLayer{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "CTF-72",
		Rules: []*pb.InterestRule{{Id: "command", Locked: true, BoostLabels: []string{"submarine"}}}}
	shipLayer := &pb.PolicyLayer{
		Echelon:  pb.Echelon_ECHELON_SHIP,
		IssuedBy: "spoof",
		Rules:    []*pb.InterestRule{{Id: "ship", Author: "spoof", BoostLabels: []string{"tanker"}}},
		Emcon:    &pb.EmconSetting{Mode: pb.EmconMode_EMCON_MODE_SILENT, SetBy: "spoof", Echelon: pb.Echelon_ECHELON_COMMAND},
	}
	svc, _ := makeOperatorService(t, &pb.Policy{Layers: []*pb.PolicyLayer{commandLayer}})
	request := operatorRequest(t, &pb.Policy{
		IssuedBy: "spoof",
		Layers: []*pb.PolicyLayer{
			proto.Clone(commandLayer).(*pb.PolicyLayer),
			shipLayer,
		},
	}, "operator", "ops-1", pb.Echelon_ECHELON_SHIP)
	response, err := svc.SetPolicy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	got := response.Msg.Policy
	if got.IssuedBy != "ops-1" {
		t.Fatalf("policy IssuedBy=%q, want ops-1", got.IssuedBy)
	}
	if len(got.Layers) != 2 {
		t.Fatalf("layers=%+v", got.Layers)
	}
	ship := got.Layers[1]
	if ship.IssuedBy != "ops-1" || ship.Rules[0].Author != "ops-1" ||
		ship.Emcon.SetBy != "ops-1" || ship.Emcon.Echelon != pb.Echelon_ECHELON_SHIP {
		t.Fatalf("ship layer attribution=%+v", ship)
	}
	if got.EmconSetting == nil || got.EmconSetting.Echelon != pb.Echelon_ECHELON_SHIP {
		t.Fatalf("flattened EMCON attribution=%+v", got.EmconSetting)
	}
}
