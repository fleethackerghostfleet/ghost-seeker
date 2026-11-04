package command

import (
	"reflect"
	"sort"
	"testing"
)

func TestParseDirective(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		rules []Rule
	}{
		{
			name: "suppress fast boats",
			text: "ignore fast boats",
			rules: []Rule{
				{Type: "suppress", Classes: []string{"small boat"}, Attrs: []string{"moving"}},
			},
		},
		{
			name: "prioritize boats and ships",
			text: "prioritize small boats and ships",
			rules: []Rule{
				{Type: "boost", Classes: []string{"small boat", "warship", "cargo ship", "tanker", "fishing vessel", "submarine"}},
			},
		},
		{
			name: "helicopters and aircraft",
			text: "helicopters and aircraft",
			rules: []Rule{
				{Type: "boost", Classes: []string{"helicopter", "military aircraft", "civil aircraft", "drone"}},
			},
		},
		{
			name: "split boost and suppression clauses",
			text: "Small boats approaching the pier at speed; ignore birds",
			rules: []Rule{
				{Type: "boost", Classes: []string{"small boat"}, Attrs: []string{"approaching", "moving"}},
				{Type: "suppress", Classes: []string{"bird"}},
			},
		},
		{
			name: "only drones and helicopters",
			text: "Only drones and helicopters",
			rules: []Rule{
				{Type: "boost", Classes: []string{"drone", "helicopter"}, Only: true},
			},
		},
		{
			name: "ships without AIS",
			text: "Ships not broadcasting AIS",
			rules: []Rule{
				{Type: "boost", Classes: []string{"warship", "cargo ship", "tanker", "fishing vessel", "small boat", "submarine"}, Attrs: []string{"dark"}},
			},
		},
		{
			name: "unknown language",
			text: "hello world",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseDirective(test.text)
			if len(got) != len(test.rules) {
				t.Fatalf("ParseDirective() returned %d rules, want %d: %#v", len(got), len(test.rules), got)
			}
			for i := range test.rules {
				if got[i].Type != test.rules[i].Type || got[i].Only != test.rules[i].Only {
					t.Errorf("rule %d = type %q only %t, want type %q only %t", i, got[i].Type, got[i].Only, test.rules[i].Type, test.rules[i].Only)
				}
				assertStringsEqual(t, "classes", got[i].Classes, test.rules[i].Classes)
				assertStringsEqual(t, "attrs", got[i].Attrs, test.rules[i].Attrs)
			}
		})
	}
}

func assertStringsEqual(t *testing.T, field string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
