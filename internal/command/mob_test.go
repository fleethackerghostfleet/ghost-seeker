package command

import (
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestMOBClassAndDirective(t *testing.T) {
	for _, text := range []string{"prioritize man overboard", "prioritize MOB", "person in the water"} {
		rules := ParseDirective(text)
		if len(rules) != 1 || len(rules[0].Classes) != 1 || rules[0].Classes[0] != "person in water" {
			t.Fatalf("%q = %+v", text, rules)
		}
	}
	for model := range catalog.Models {
		if w := ContactWeight(Detection{Class: "person in water", Conf: .9}, model); w != 1 {
			t.Errorf("%s MOB weight = %v", model, w)
		}
	}
	for scene, pool := range catalog.Pools {
		if _, simulated := pool["person in water"]; simulated {
			t.Errorf("%s simulation pool must not invent live-only MOB contacts", scene)
		}
	}
	if !RuleMatch(Rule{Classes: []string{"person"}}, Detection{Class: "person in water"}) {
		t.Fatal("existing person directives must match live MOB refinement")
	}
	classes := []string{}
	for class := range catalog.Classes {
		if class != "person in water" && len(classes) < 14 {
			classes = append(classes, class)
		}
	}
	if _, blocked := guardrails("", []Rule{{Type: "boost", Classes: classes}}, nil, nil); !blocked {
		t.Fatal("adding MOB diluted the existing 14-class broad boost guard")
	}
}
