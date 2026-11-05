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

func TestLivePreviewHonorsTierPins(t *testing.T) {
	c := NewConsole(New("", "", nil))
	card := ToCard(&pb.ItemView{Item: &pb.Item{Id: "contact", Features: &pb.Features{Detections: []*pb.Detection{{Label: "boat", Confidence: 1}}, Tags: map[string]string{"subtype": "tanker"}}}})
	o := &Object{Key: "contact", Label: "boat", Subtype: "tanker", Best: card, Items: []*Card{card}, Score: 100}
	c.onSnapshot(&Snapshot{Objects: []*Object{o}}, nil)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/directives/preview", strings.NewReader(body)))
		return w
	}
	for tier := 1; tier <= 4; tier++ {
		body := fmt.Sprintf(`{"rules":[{"type":"suppress","classes":["tanker"],"tier":%d}],"candidates":[{"key":"raise","rules":[{"type":"boost","classes":["tanker"],"tier":1}]},{"key":"generic","rules":[{"type":"suppress","classes":["tanker"]}]}]}`, tier)
		w := post(body)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var got struct {
			Items      []previewItem     `json:"items"`
			Counts     [4]int            `json:"counts"`
			Candidates []candidateResult `json:"candidates"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Items[0].Tier != tier || got.Counts[tier-1] != 1 || got.Candidates[0].Counts[0] != 1 || got.Candidates[1].Counts[3] != 1 {
			t.Fatalf("P%d preview incorrect: %+v", tier, got)
		}
	}
	if c.base["contact"].Tier != 1 || len(c.policy.TierLabels) != 0 {
		t.Fatal("preview mutated standing state")
	}
	c.overlay["contact"] = &overlay{Manual: 3}
	w := post(`{"rules":[{"type":"suppress","classes":["tanker"],"tier":4}]}`)
	if !strings.Contains(w.Body.String(), `"tier":3`) {
		t.Fatalf("manual tier lost: %s", w.Body.String())
	}
	for _, body := range []string{`{"rules":[{"tier":5}]}`, `{"candidates":[{"rules":[{"tier":-1}]}]}`, `{`} {
		if w := post(body); w.Code != 400 {
			t.Fatalf("invalid preview: %d %s", w.Code, w.Body.String())
		}
	}
	empty := NewConsole(New("", "", nil))
	w = httptest.NewRecorder()
	empty.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/directives/preview", strings.NewReader(`{"rules":[]}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("empty preview: %d %s", w.Code, w.Body.String())
	}
}
