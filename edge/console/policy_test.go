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
)

func TestSetPolicyErrorMappingAndAuditHandler(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	sch, err := scheduler.New(scheduler.DefaultConfig("edge"), st, link.NewConnect("http://localhost:8080", time.Minute, link.NewEstimator(1000, 1, 1e9)),
		scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{NodeID: "edge", St: st, Sch: sch, Clk: clk}
	_, err = svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "v3", Version: 3}}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SetPolicy(context.Background(), connect.NewRequest(&pb.SetPolicyRequest{Policy: &pb.Policy{Id: "stale", Version: 3}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale policy code=%v err=%v", connect.CodeOf(err), err)
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
