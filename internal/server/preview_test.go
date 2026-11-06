package server

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestPreviewDoesNotMutate(t *testing.T) {
	data := store.New(10)
	item := command.Item{ID: "boat", SourceID: "EO-P7", TS: time.Now(), Dets: []command.Detection{{Class: "small boat", Conf: .9}}}
	data.Add(&item, 0)
	item.ID = "pinned"
	item.Manual = 4
	data.Add(&item, 0)
	data.AddDirective("active", []command.Rule{{Type: "suppress", Classes: []string{"bird"}}})
	before, stats, cdr, state := data.Snapshot()
	app := New(data)
	response := request(app, "POST", "/api/directives/preview", map[string]any{"text": "prioritize purple boats", "candidates": []any{map[string]any{"key": "suppress", "rules": []command.Rule{{Type: "suppress", Classes: []string{"small boat"}}}}}})
	if response.Code != 200 {
		t.Fatalf("preview: %d %s", response.Code, response.Body)
	}
	var result struct {
		Rules      []command.Rule
		Unknown    []string
		Items      []previewItem
		Counts     [4]int
		Candidates []candidateResult
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 1 || !reflect.DeepEqual(result.Unknown, []string{"purple"}) || len(result.Items) != 2 || result.Items[0].Central <= before[0].Central || result.Items[1].Tier != 4 || len(result.Items[0].Contrib) != 1 || result.Items[0].Contrib[0].Delta <= 0 || result.Candidates[0].Changed != 1 {
		t.Fatalf("preview result: %+v", result)
	}
	after, s2, c2, state2 := data.Snapshot()
	if !reflect.DeepEqual(before, after) || stats != s2 || cdr != c2 || !reflect.DeepEqual(state, state2) {
		t.Fatal("preview mutated store")
	}
	response = request(app, "POST", "/api/directives/preview", map[string]any{})
	if response.Code != 200 {
		t.Fatalf("empty preview: %d", response.Code)
	}
	response = request(app, "POST", "/api/directives/preview", map[string]any{"rules": []command.Rule{{Type: "boost", Sources: []string{"invalid"}}}})
	if response.Code != 400 {
		t.Fatalf("invalid scope status: %d", response.Code)
	}
	response = request(app, "POST", "/api/directives/preview", map[string]any{"rules": []command.Rule{{Type: "boost", Weight: -1}}})
	if response.Code != 400 {
		t.Fatalf("invalid weight status: %d", response.Code)
	}
}

func TestSimulationWhaleDirective(t *testing.T) {
	data := store.New(10)
	data.Add(&command.Item{ID: "whale", SourceID: "EO-P7", TS: time.Now(), Dets: []command.Detection{{Class: "marine mammal", Conf: .95}}}, 0)
	before := data.Items()[0]
	response := request(New(data), "POST", "/api/directives", map[string]string{"text": "prioritize whales"})
	if response.Code != 201 {
		t.Fatalf("directive: %d %s", response.Code, response.Body)
	}
	after := data.Items()[0]
	if after.Central <= before.Central || after.Tier >= before.Tier {
		t.Fatalf("whale did not rise: before score=%v tier=%d, after score=%v tier=%d", before.Central, before.Tier, after.Central, after.Tier)
	}
}

func TestExplicitDirectiveRulesOverrideText(t *testing.T) {
	data := store.New(2)
	app := New(data)
	rule := command.Rule{Type: "suppress", Classes: []string{"small boat"}, Weight: 2, Sources: []string{"EO-P7"}, WithinMin: 30, Edited: true}
	response := request(app, "POST", "/api/directives", map[string]any{"text": "prioritize birds", "rules": []command.Rule{rule}})
	if response.Code != 201 {
		t.Fatalf("post: %d %s", response.Code, response.Body)
	}
	_, _, _, state := data.Snapshot()
	got := state.Directives[0].Rules[0]
	got.DirectiveID = ""
	rule.Attrs = []string{}
	if !reflect.DeepEqual(got, rule) {
		t.Fatalf("got %+v want %+v", got, rule)
	}
	response = request(app, "POST", "/api/directives", map[string]any{"text": "prioritize boats", "rules": []command.Rule{}})
	if response.Code != 422 {
		t.Fatalf("empty explicit rules must not parse text: %d", response.Code)
	}
	response = request(app, "POST", "/api/directives", map[string]any{"rules": []command.Rule{{Type: "broken"}}})
	if response.Code != 400 {
		t.Fatalf("invalid explicit rule: %d", response.Code)
	}
}

func TestPreviewExcludesDismissedItems(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dismissed []bool
		visible   int
	}{
		{"mixed", []bool{true, false, true, false}, 2},
		{"all dismissed", []bool{true, true}, 0},
		{"empty", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := store.New(10)
			for i, dismissed := range tc.dismissed {
				item := command.Item{
					ID: strconv.Itoa(i), Dismissed: dismissed,
					Dets: []command.Detection{{Class: "small boat", Conf: .9}},
				}
				data.Add(&item, 0)
			}
			before := data.Items()
			response := request(New(data), "POST", "/api/directives/preview", map[string]any{
				"text": "prioritize boats",
				"candidates": []any{map[string]any{
					"key": "suppress", "rules": []command.Rule{{Type: "suppress", Classes: []string{"small boat"}}},
				}},
			})
			if response.Code != 200 {
				t.Fatalf("preview: %d %s", response.Code, response.Body)
			}
			var result struct {
				Items      []previewItem
				Counts     [4]int
				Candidates []candidateResult
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != tc.visible || result.Counts != [4]int{tc.visible, 0, 0, 0} {
				t.Fatalf("items/counts include dismissed images: %+v", result)
			}
			for _, item := range result.Items {
				stored, ok := data.GetItem(item.ID)
				if !ok || stored.Dismissed {
					t.Fatalf("dismissed or unknown preview item: %q", item.ID)
				}
			}
			if len(result.Candidates) != 1 || result.Candidates[0].Counts != [4]int{0, 0, 0, tc.visible} || result.Candidates[0].Changed != tc.visible {
				t.Fatalf("candidate counts/changes include dismissed images: %+v", result.Candidates)
			}
			if !reflect.DeepEqual(before, data.Items()) {
				t.Fatal("preview filtering mutated stored items")
			}
		})
	}
}
