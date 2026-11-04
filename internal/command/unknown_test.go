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
