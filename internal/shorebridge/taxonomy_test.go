package shorebridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestFishingTaxonomyPreviewAndPolicyTransport(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
	console := NewConsole(New(shore.server.URL, "", nil))
	console.base["fish"] = &command.Item{ID: "fish", Central: .4, Tier: 3, Live: &command.LiveMeta{Subtype: "fishing boat"}}
	console.base["human"] = &command.Item{ID: "human", Central: .4, Tier: 3, Live: &command.LiveMeta{Subtype: "fishing boat", Override: "cargo ship"}}
	console.base["unknown"] = &command.Item{ID: "unknown", Central: .8, Tier: 1, Live: &command.LiveMeta{Subtype: "fishing boat", Unknown: true}}
	console.order = []string{"fish", "human", "unknown"}
	handler := console.Handler()
	preview := func(action string) []previewItem {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/directives/preview", strings.NewReader(`{"rules":[{"type":"`+action+`","classes":["fishing vessel"]}]}`)))
		if rec.Code != 200 {
			t.Fatalf("preview status %d: %s", rec.Code, rec.Body)
		}
		var result struct {
			Items []previewItem
			Rules []command.Rule
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Rules) != 1 || result.Rules[0].Classes[0] != "fishing vessel" {
			t.Fatalf("transport alias leaked into UI rules: %+v", result.Rules)
		}
		return result.Items
	}
	boosted := preview("boost")
	if boosted[0].Tier != 2 || boosted[1].Tier != 3 {
		t.Fatalf("boost preview does not respect subtype/override: %+v", boosted)
	}
	suppressed := preview("suppress")
	if suppressed[0].Tier != 4 || suppressed[1].Tier != 3 || suppressed[2].Tier != 1 {
		t.Fatalf("suppression changed protected human/unknown evidence: %+v", suppressed)
	}
	postDirective(t, handler, `{"text":"Fishing preference","rules":[{"type":"boost","classes":["fishing vessel"]}]}`)
	stored := shore.stored()
	want := []string{"fishing vessel", "fishing boat"}
	if !reflect.DeepEqual(stored.GetInterest().GetBoostLabels(), want) {
		t.Fatalf("SetPolicy labels %v, want %v", stored.GetInterest().GetBoostLabels(), want)
	}
	if got := Readback(nil, stored.GetInterest().GetBoostLabels()); strings.Contains(got, "fishing boat") || strings.Count(got, "fishing vessel") != 1 {
		t.Fatalf("transport alias leaked into readback: %s", got)
	}
	ctx := &scoring.Context{Policy: stored, Now: time.Now()}
	fish := &pb.Item{CapturedAtUnixMs: ctx.Now.UnixMilli(), Features: &pb.Features{Tags: map[string]string{"subtype": "fishing boat"}}}
	if got := (scoring.Interest{}).Score(fish, ctx); got.Value <= 0 || got.Multiplier <= 1 {
		t.Fatalf("actual edge scorer missed transported subtype: %+v", got)
	}
	fish.Features.LabelOverride = "cargo ship"
	if got := (scoring.Interest{}).Score(fish, ctx); got.Value != 0 {
		t.Fatalf("actual edge scorer ignored human override: %+v", got)
	}
	fish.Features.LabelOverride = ""
	fish.Features.Unknownness = .9
	ctx.Policy = &pb.Policy{Interest: &pb.Interest{SuppressLabels: want}}
	if got := (scoring.Interest{}).Score(fish, ctx); got.Suppressed {
		t.Fatal("transport alias bypassed unknown protection")
	}
	if got := transportLabels([]string{"red fishing boat", "fishing net"}); !reflect.DeepEqual(got, []string{"red fishing boat", "fishing net"}) {
		t.Fatalf("taxonomy adapter broadened novel concepts: %v", got)
	}
}

func TestFishingTaxonomyPreservesHigherPolicyLocks(t *testing.T) {
	active := &pb.Policy{Layers: []*pb.PolicyLayer{{Echelon: pb.Echelon_ECHELON_COMMAND, IssuedBy: "Command", Rules: []*pb.InterestRule{{Id: "fish-lock", Locked: true, BoostLabels: []string{"fishing boat"}}}}}}
	shore := newStatefulShore(t, active)
	bridge := New(shore.server.URL, "", nil)
	_, blocked := bridge.PolicyLocks().CheckLocks([]command.Rule{{Type: "suppress", Classes: []string{"fishing vessel"}}})
	if !blocked {
		t.Fatal("demo taxonomy bypassed higher policy lock on sidecar taxonomy")
	}
}
