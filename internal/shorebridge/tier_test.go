package shorebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestRulesFromBodyPreservesTier(t *testing.T) {
	for _, kind := range []string{"boost", "suppress"} {
		rs := []command.Rule{{Type: kind, Classes: []string{" Tanker "}, Tier: 2}}
		s, b, parsed := rulesFromBody("", &rs)
		if len(s)+len(b) != 0 || ruleTiers(parsed)["tanker"] != 2 {
			t.Fatalf("pin became generic: %v %v %v", s, b, parsed)
		}
		rs = append(rs, command.Rule{Type: "suppress", Classes: []string{"tanker"}})
		s, _, parsed = rulesFromBody("", &rs)
		if len(ruleTiers(parsed)) != 0 || len(s) != 1 {
			t.Fatalf("later generic must cancel pin: %v", parsed)
		}
	}
	s, b, rs := rulesFromBody("", nil)
	if len(s)+len(b)+len(rs) != 0 {
		t.Fatal("empty directive produced rules")
	}
}

func TestAssignPinnedTierExcludesObjectFromRankBands(t *testing.T) {
	var objects []*Object
	for i := 0; i < 10; i++ {
		objects = append(objects, &Object{Label: "other", Score: float64(10 - i), Best: &Card{}})
	}
	objects[0].Label = " Tanker "
	assignTiers(objects, map[string]int32{"tanker": 2})
	if objects[0].Priority != "P2" || objects[1].Priority != "P1" || objects[3].Priority != "P2" || objects[4].Priority != "P3" {
		t.Fatalf("pins did not re-band: %s %s %s %s", objects[0].Priority, objects[1].Priority, objects[3].Priority, objects[4].Priority)
	}
	assignTiers(nil, map[string]int32{"tanker": 2})
	assignTiers(objects, map[string]int32{"tanker": 5})
	if objects[0].Priority != "P1" {
		t.Fatal("invalid pin changed rank", objects[0])
	}
}

func TestLiveConsoleSuggestionPinsWholeClass(t *testing.T) {
	shore := newStatefulShore(t, &pb.Policy{})
	for _, id := range []string{"tanker-a", "tanker-b", "bird"} {
		v := view(id, time.Now().UnixMilli(), nil, map[pb.FormKind]bool{pb.FormKind_FORM_KIND_THUMB: true})
		label := "tanker"
		if id == "bird" {
			label = "bird"
			v.Score.Priority = .1
		} else {
			v.Score.Priority = 10
		}
		v.Item.Features.Detections[0].Label = label
		shore.items = append(shore.items, v)
	}
	bridge := New(shore.server.URL, "", nil)
	console := NewConsole(bridge)
	bridge.refresh(context.Background())
	body := `{"text":"Operator feedback: lower Tanker to P2 High","rules":[{"type":"suppress","classes":["tanker"],"attrs":[],"only":false,"tier":2}]}`
	response := postDirective(t, console.Handler(), body)
	if !strings.Contains(response["readback"].(string), "Set tanker → P2 High") {
		t.Fatal(response)
	}
	stored := shore.stored()
	if stored.Interest.TierLabels["tanker"] != 2 || len(stored.Interest.SuppressLabels)+len(stored.Interest.BoostLabels) != 0 {
		t.Fatal(stored)
	}
	bridge.refresh(context.Background())
	console.mu.Lock()
	for _, item := range console.base {
		if item.Live.Label == "tanker" && (item.Tier != 2 || item.CentralTier != 2 || item.Live.Suppressed) {
			t.Fatalf("suggestion lost target: %+v", item)
		}
		if item.Live.Label == "bird" && item.Tier != 1 {
			t.Fatalf("unpinned object did not re-band: %+v", item)
		}
	}
	if len(console.base) != 3 {
		t.Fatalf("missing live items: %d", len(console.base))
	}
	console.mu.Unlock()
	var policyView struct {
		Policy LivePolicy `json:"policy"`
	}
	getJSON(t, console.Handler(), "/api/policy", &policyView)
	if !strings.Contains(policyView.Policy.Readback, "Set tanker → P2 High") {
		t.Fatal(policyView)
	}
	for _, tier := range []int{-1, 5} {
		bad, _ := json.Marshal(map[string]any{"rules": []command.Rule{{Type: "suppress", Classes: []string{"tanker"}, Tier: tier}}})
		rec := httptest.NewRecorder()
		console.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/directives", strings.NewReader(string(bad))))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid tier accepted: %d %s", rec.Code, rec.Body)
		}
	}
	postDirective(t, console.Handler(), `{"text":"ignore tankers","rules":[{"type":"suppress","classes":["tanker"]}]}`)
	if pins := shore.stored().Interest.TierLabels; len(pins) != 0 {
		t.Fatal("new generic did not cancel old pin", pins)
	}
}

func TestShoreTierPinsMatchEdgeLabels(t *testing.T) {
	for _, tc := range []struct {
		name, override, subtype string
		conf, min               float32
		pins                    map[string]int32
		want                    int
	}{
		{"subtype", "", "cargo ship", .9, 0, map[string]int32{"cargo ships": 4}, 4},
		{"urgent wins", "", "cargo_ship", .9, 0, map[string]int32{"boat": 3, "cargo ships": 1}, 1},
		{"other detection", "", "", .9, 0, map[string]int32{"tanker": 2}, 2},
		{"below threshold", "", "", .3, .5, map[string]int32{"tanker": 4}, 0},
		{"override stands alone", "bird", "cargo ship", .9, 0, map[string]int32{"cargo ships": 4, "tanker": 4}, 0},
		{"override match", "bird", "cargo ship", .9, 0, map[string]int32{"bird": 3, "cargo ships": 1}, 3},
		{"invalid", "", "cargo ship", .9, 0, map[string]int32{"cargo ships": 5}, 0},
		{"empty", "", "", .9, 0, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &pb.ItemView{Item: &pb.Item{Id: "contact", Features: &pb.Features{LabelOverride: tc.override, Detections: []*pb.Detection{{Label: "boat", Confidence: 1}, {Label: "tanker", Confidence: tc.conf}}, Tags: map[string]string{"subtype": tc.subtype}}}}
			card := ToCard(v)
			o := &Object{Key: "contact", Label: "boat", Subtype: tc.subtype, Best: card, Items: []*Card{card}, Score: 100, detectionMin: tc.min}
			pol := &pb.Policy{Interest: &pb.Interest{TierLabels: tc.pins}, Thresholds: &pb.Thresholds{DetectionMinConf: tc.min}}
			_, edgeTier := scoring.TierPin(v.Item, pol)
			if int(edgeTier) != tc.want {
				t.Fatalf("edge tier=%d want=%d", edgeTier, tc.want)
			}
			assignTiers([]*Object{o}, tc.pins)
			scores := scoreObjects([]*Object{o}, tc.pins)
			want := tc.want
			if want == 0 {
				want = 1
			}
			if o.Priority != fmt.Sprintf("P%d", want) || scores[o].centralTier != want {
				t.Fatalf("shore disagrees: %v %v", o, scores[o])
			}
			c := NewConsole(New("", "", nil))
			c.policy.TierLabels = tc.pins
			item := c.buildItem(o, scores[o])
			if item.CentralTier != want {
				t.Fatal(item)
			}
			if tc.want != 0 {
				found := false
				for _, note := range item.Det[0].Notes {
					if note.Tier == tc.want {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing pinned note: %v", item.Det)
				}
			}
		})
	}
}
