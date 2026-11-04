package command

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestWorkbenchDefaultsAndWeights(t *testing.T) {
	for _, kind := range []string{"boost", "suppress"} {
		d := Detection{Class: "small boat", Conf: .9}
		rule := Rule{Type: kind, Classes: []string{"small boat"}}
		legacy := ScoreDet(d, []Rule{rule}, "maritime").Score
		rule.Weight = 1
		if got := ScoreDet(d, []Rule{rule}, "maritime").Score; math.Float64bits(got) != math.Float64bits(legacy) {
			t.Fatalf("default differs: %v != %v", got, legacy)
		}
		expected := catalog.Models["maritime"].W["small boat"]
		if kind == "boost" {
			expected = 1 - (1-expected)*.22
		} else {
			expected *= .12
		}
		if legacy != min(.99, expected*ConfFactor(d.Conf)) {
			t.Fatalf("%s legacy score changed: %v", kind, legacy)
		}
		rule.Weight = 2
		got := ScoreDet(d, []Rule{rule}, "maritime").Score
		if kind == "boost" && got <= legacy || kind == "suppress" && got >= legacy {
			t.Fatalf("%s weight had wrong effect", kind)
		}
	}
}

func TestWorkbenchScopedRules(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	rule := Rule{Type: "boost", Classes: []string{"small boat"}, Sources: []string{"EO-P7", "UAV-3"}, WithinMin: 30}
	for _, tc := range []struct {
		source  string
		age     int
		matches bool
	}{{"EO-P7", 5, true}, {"UAV-3", 30, true}, {"EO-P7", 31, false}, {"BY-12", 5, false}} {
		item := Item{SourceID: tc.source, TS: now.Add(-time.Duration(tc.age) * time.Minute), Dets: []Detection{{Class: "small boat", Conf: .9}}}
		base := item
		RescoreAt(&base, "maritime", nil, now)
		RescoreAt(&item, "maritime", []Directive{{Rules: []Rule{rule}}}, now)
		if (item.Central > base.Central) != tc.matches {
			t.Fatalf("%+v: score %v base %v", tc, item.Central, base.Central)
		}
		item.Manual = 4
		RescoreAt(&item, "maritime", []Directive{{Rules: []Rule{rule}}}, now)
		if item.Tier != 4 {
			t.Fatal("manual lost")
		}
	}
}

func TestParseDirectiveDetailed(t *testing.T) {
	rules, unknown := ParseDirectiveDetailed("prioritize purple boats from EO-P7 last 30 min; suppress birds from IR Thermal · Harbor Entrance past hour")
	if len(rules) != 2 || rules[0].WithinMin != 30 || rules[1].WithinMin != 60 || !reflect.DeepEqual(rules[0].Sources, []string{"EO-P7"}) || !reflect.DeepEqual(rules[1].Sources, []string{"IR-HE"}) {
		t.Fatalf("rules = %+v", rules)
	}
	if !reflect.DeepEqual(unknown, []string{"purple"}) {
		t.Fatalf("unknown = %v", unknown)
	}
	if !reflect.DeepEqual(rules, ParseDirective("prioritize purple boats from EO-P7 last 30 min; suppress birds from IR Thermal · Harbor Entrance past hour")) {
		t.Fatal("wrapper differs")
	}
	rules, unknown = ParseDirectiveDetailed("")
	if len(rules) != 0 || len(unknown) != 0 {
		t.Fatal("empty parse is not empty")
	}
	_, unknown = ParseDirectiveDetailed("purple nonsense")
	if len(unknown) != 2 {
		t.Fatalf("unknown-only parse: %v", unknown)
	}
}
