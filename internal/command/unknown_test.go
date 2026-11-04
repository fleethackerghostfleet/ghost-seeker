package command

import "testing"

func TestUnknownAliases(t *testing.T) {
	for _, text := range []string{"unknown objects", "unknown contacts", "unidentified contacts", "unclassified"} {
		rules, unused := ParseDirectiveDetailed("Prioritize " + text)
		if len(rules) != 1 || len(rules[0].Classes) != 1 || rules[0].Classes[0] != "unknown" || len(rules[0].Attrs) != 0 || len(unused) != 0 {
			t.Errorf("%q: %+v unused %v", text, rules, unused)
		}
	}
	rules := ParseDirective("unknown vessel")
	if len(rules) != 1 || !contains(rules[0].Attrs, "dark") || contains(rules[0].Classes, "unknown") {
		t.Fatalf("unknown vessel changed: %+v", rules)
	}
	if got := ParseDirective(""); len(got) != 0 {
		t.Fatalf("empty directive: %+v", got)
	}
}

func TestRuleMatchTransient(t *testing.T) {
	rule := Rule{Type: "suppress", Classes: []string{"unknown"}, Attrs: []string{"transient"}}
	for _, tc := range []struct {
		name string
		det  Detection
		want bool
	}{
		{"transient unknown", Detection{Class: "unknown", Attrs: Attributes{Transient: true}}, true},
		{"persistent unknown", Detection{Class: "unknown"}, false},
		{"different class", Detection{Class: "bird", Attrs: Attributes{Transient: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RuleMatch(rule, tc.det); got != tc.want {
				t.Fatalf("RuleMatch = %v, want %v", got, tc.want)
			}
		})
	}
}
