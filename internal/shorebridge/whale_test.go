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

func TestWhalePreviewCompileAndApply(t *testing.T) {
	for _, tc := range []struct{ text, action string }{
		{"whales spotted", "boost"},
		{"whale near the ship", "boost"},
		{"prioritize whales", "boost"},
		{"look for marine mammals", "boost"},
		{"ignore whales", "suppress"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			_, _, interpreted := rulesFromBody(tc.text, nil)
			if len(interpreted) != 1 || interpreted[0].Type != tc.action || !reflect.DeepEqual(interpreted[0].Classes, []string{"marine mammal"}) {
				t.Fatalf("interpreter rules: %+v", interpreted)
			}
			shore := newStatefulShore(t, &pb.Policy{Interest: &pb.Interest{}})
			bridge := New(shore.server.URL, "", nil)
			bridge.Compiler = &command.Compiler{EdgeSubtypes: true}
			console := NewConsole(bridge)
			console.base["whale"] = &command.Item{ID: "whale", Central: .4, Tier: 3, Base: .4, BaseTier: 3, Live: &command.LiveMeta{Label: "marine mammal"}}
			console.order = []string{"whale"}
			handler := console.Handler()
			body := `{"text":"` + tc.text + `"}`
			var previewRules []command.Rule
			for _, endpoint := range []string{"/api/directives/preview", "/api/directives/compile"} {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
				var result struct {
					Rules   []command.Rule
					Items   []previewItem
					Blocked bool
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if rec.Code != http.StatusOK || result.Blocked || len(result.Rules) != 1 || result.Rules[0].Type != tc.action || !reflect.DeepEqual(result.Rules[0].Classes, []string{"marine mammal"}) {
					t.Fatalf("%s: %d %s", endpoint, rec.Code, rec.Body)
				}
				if endpoint == "/api/directives/preview" {
					previewRules = result.Rules
					if tc.action == "boost" && (result.Items[0].Central <= .4 || result.Items[0].Tier >= 3) {
						t.Fatalf("marine mammal did not rise: %+v", result.Items)
					}
				} else if !reflect.DeepEqual(previewRules, result.Rules) {
					t.Fatalf("preview/compile mismatch: %+v / %+v", previewRules, result.Rules)
				}
			}
			now := time.Now()
			item := &pb.Item{CapturedAtUnixMs: now.UnixMilli(), Features: &pb.Features{Tags: map[string]string{"subtype": "marine mammal"}}}
			ctx := &scoring.Context{Policy: shore.stored(), Now: now}
			before := (scoring.Interest{}).Score(item, ctx)
			postDirective(t, handler, body)
			ctx.Policy = shore.stored()
			suppress, boost, _ := effective(ctx.Policy)
			if tc.action == "suppress" {
				if !sameLabels(suppress, "marine mammal") || len(boost) != 0 {
					t.Fatalf("applied suppress=%v boost=%v", suppress, boost)
				}
			} else {
				if !sameLabels(boost, "marine mammal") || len(suppress) != 0 {
					t.Fatalf("applied suppress=%v boost=%v", suppress, boost)
				}
				if after := (scoring.Interest{}).Score(item, ctx); after.Value <= before.Value || after.Multiplier <= before.Multiplier {
					t.Fatalf("edge score did not rise: before=%+v after=%+v", before, after)
				}
			}
		})
	}
}
