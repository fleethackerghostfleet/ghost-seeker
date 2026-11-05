package console

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"google.golang.org/protobuf/proto"
)

func TestSetPolicyErrorMappingAndAuditHandler(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg := scheduler.DefaultConfig("edge")
	sch, err := scheduler.New(cfg, st, link.NewConnect("http://localhost:8080", time.Minute, link.NewEstimator(1000, 1, 1e9)),
		scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{NodeID: "edge", St: st, Sch: sch, Clk: clk}
	_, err = svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "v3", Version: 3}}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "command",
		Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "command"}}}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unsigned command policy code=%v err=%v", connect.CodeOf(err), err)
	}
	response := httptest.NewRecorder()
	svc.PolicyAuditHandler().ServeHTTP(response, httptest.NewRequest("GET", "/policy/audit?limit=2", nil))
	var body struct {
		Entries []struct {
			PolicyID string `json:"policyId"`
			Result   string `json:"result"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(body.Entries) != 2 || body.Entries[0].Result != "rejected" || body.Entries[1].Result != "applied" {
		t.Fatalf("HTTP status=%d body=%+v", response.Code, body)
	}
}

func TestSetPolicyAssignsLocalVersionsAndAcceptsSignedPolicies(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(now)
	pub, priv, err := policy.GenerateKey("shore")
	if err != nil {
		t.Fatal(err)
	}
	cfg := scheduler.DefaultConfig("edge")
	cfg.PolicyKeys = policy.Keyring{"shore": pub}
	sch, err := scheduler.New(cfg, st, link.NewConnect("http://localhost:8080", time.Minute, link.NewEstimator(1000, 1, 1e9)),
		scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{NodeID: "edge", St: st, Sch: sch, Clk: clk}
	unsignedCommand := &pb.Policy{Id: "unsigned-command", Version: int64(^uint64(0) >> 1),
		Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "command"}}}
	if _, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: unsignedCommand})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unsigned command code=%v err=%v", connect.CodeOf(err), err)
	}
	unsignedShip := &pb.Policy{Id: "unsigned-ship", Version: int64(^uint64(0) >> 1)}
	response, err := svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: unsignedShip}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.Policy.Version < now.UnixMilli() || response.Msg.Policy.Version > policy.MaxVersion {
		t.Fatalf("local version=%d outside bounds", response.Msg.Policy.Version)
	}
	_, highWater, err := st.LoadPolicyState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if highWater != 0 {
		t.Fatalf("unsigned local write advanced highwater to %d", highWater)
	}
	signed := &pb.Policy{Id: "signed-downlink", Version: now.UnixMilli(), IssuedAtUnixMs: now.UnixMilli()}
	if err := policy.Sign(signed, "shore", priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sch.AcceptPolicy(context.Background(), signed, "downlink"); err != nil {
		t.Fatalf("signed downlink rejected: %v", err)
	}
	command := &pb.Policy{Id: "signed-command", Version: now.UnixMilli() + 1, IssuedAtUnixMs: now.UnixMilli(),
		Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "command"}}}
	if err := policy.Sign(command, "shore", priv); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sch.AcceptPolicy(context.Background(), command, "downlink"); err != nil {
		t.Fatalf("later signed command downlink rejected: %v", err)
	}
	_, highWater, err = st.LoadPolicyState(context.Background())
	if err != nil || highWater != command.Version {
		t.Fatalf("highwater=%d err=%v, want signed version %d", highWater, err, command.Version)
	}
	if !proto.Equal(sch.Policy().Layers[0], command.Layers[0]) {
		t.Fatalf("signed command layer not applied: %+v", sch.Policy().Layers)
	}
}
