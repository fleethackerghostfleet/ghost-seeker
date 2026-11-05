package console

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

func TestShipPolicyHandler(t *testing.T) {
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
	h := (&Service{NodeID: "edge", St: st, Sch: sch, Clk: clk}).ShipPolicyHandler()
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/ship/policy", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"rules":[{"boost":["submarine"],"suppress":["cruise ship"]}],"emcon":"BURST"}`); rec.Code != 200 {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	p := sch.Policy()
	if p.Interest.BoostLabels[0] != "submarine" || p.Interest.SuppressLabels[0] != "cruise ship" || p.Emcon.String() != "EMCON_MODE_BURST" {
		t.Fatalf("applied policy %v", p)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ship/policy", nil))
	var body struct {
		Local struct {
			Rules []struct{ BoostLabels []string } `json:"rules"`
		} `json:"local"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Local.Rules) != 1 {
		t.Fatalf("get: %v %s", err, rec.Body)
	}
	for _, bad := range []string{`{"rules":[{}]}`, `{"rules":[],"emcon":"LOUD"}`, `{"rules":[{"boost":["` + strings.Repeat("x", 200) + `"]}]}`} {
		if rec := post(bad); rec.Code != 400 {
			t.Fatalf("%s: code %d", bad, rec.Code)
		}
	}
	if rec := post(`{"rules":[]}`); rec.Code != 200 || len(sch.Policy().Interest.GetBoostLabels()) != 0 || sch.Policy().Emcon.String() == "EMCON_MODE_BURST" {
		t.Fatalf("clear: %d %v", rec.Code, sch.Policy())
	}
}
