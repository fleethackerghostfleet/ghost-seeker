package command

import (
	"context"
	"reflect"
	"testing"
)

func TestObservationClasses(t *testing.T) {
	for text, want := range map[string]string{
		"whales spotted":            "marine mammal",
		"whale near the ship":       "marine mammal",
		"seals spotted":             "marine mammal",
		"cargo ship near the whale": "cargo ship",
		"marine mammals":            "marine mammal",
		"whale near shore":          "", "whale near the ship and ignore birds": "",
		"": "", "unicorns spotted": "", "ignore whales": "", "whale near": "", "red whale spotted": "",
	} {
		got := ObservationClasses(text)
		if (want == "" && len(got) != 0) || (want != "" && !reflect.DeepEqual(got, []string{want})) {
			t.Errorf("%q = %v; want %q", text, got, want)
		}
	}
}

func TestGenericObservationClasses(t *testing.T) {
	compiler := &Compiler{EdgeSubtypes: true}
	for _, tc := range []struct{ text, directive string }{
		{"vessel spotted", "prioritize vessels"},
		{"aircraft near the ship", "prioritize aircraft"},
	} {
		want := compiler.CompileRules(tc.directive)
		got := compiler.CompileRules(tc.text)
		if !reflect.DeepEqual(got.Rules, want.Rules) || len(got.Unknown) != 0 {
			t.Errorf("%q = %+v; want rules %+v", tc.text, got, want.Rules)
		}
		if classes := ObservationClasses(tc.text); !reflect.DeepEqual(classes, want.Rules[0].Classes) {
			t.Errorf("%q observation = %v; want %v", tc.text, classes, want.Rules[0].Classes)
		}
	}
}

func TestObservationActions(t *testing.T) {
	for _, edge := range []bool{false, true} {
		compiler := &Compiler{EdgeSubtypes: edge}
		for _, tc := range []struct{ text, action string }{
			{"prioritize whale near the ship", "boost"},
			{"ignore whales near the ship", "suppress"},
			{"prioritize whales near the ship", "boost"},
			{"whale near the ship", "boost"},
			{"whales spotted", "boost"},
			{"whales sighted", "boost"},
			{"prioritize whales", "boost"},
			{"look for whales", "boost"},
		} {
			got := compiler.CompileRules(tc.text)
			if len(got.Rules) != 1 || got.Rules[0].Type != tc.action || !reflect.DeepEqual(got.Rules[0].Classes, []string{"marine mammal"}) || len(got.Unknown) != 0 {
				t.Errorf("%q = %+v; want %s marine mammal", tc.text, got, tc.action)
			}
		}
	}
}

func TestStripObservationContext(t *testing.T) {
	for input, want := range map[string]string{
		" PRIORITIZE  whales near THE ship ":   "prioritize whales",
		"prioritize contacts near shore":       "prioritize contacts near shore",
		"submarine near the carrier":           "submarine near the carrier",
		"unicorn near the ship":                "unicorn near the ship",
		"whale near the ship and ignore birds": "whale and ignore birds",
		"":                                     "", "whales sighted": "whales", "cargo ship near the whale": "cargo ship",
	} {
		if got := StripObservationContext(input); got != want {
			t.Errorf("%q = %q; want %q", input, got, want)
		}
	}
}

func TestWhaleDirectiveProbe(t *testing.T) {
	for _, edge := range []bool{true, false} {
		for _, text := range []string{"ignore whales near the ship", "prioritize whales near the ship", "whale near the ship", "cargo ship near the whale", "prioritize whales", "whales spotted", "look for whales", "prioritize contacts near shore", "submarine near the carrier"} {
			rules, unknown := parseDirectiveDetailed(text, edge)
			t.Logf("edgeSubtypes=%v %q -> rules=%+v unknown=%v", edge, text, rules, unknown)
		}
	}
}

func TestObservationContextQualifiers(t *testing.T) {
	for _, edge := range []bool{false, true} {
		for _, tc := range []struct {
			text, action string
			within       int
			sources      []string
		}{
			{"prioritize whales near the ship last 30 minutes", "boost", 30, nil},
			{"ignore whales near the ship from UAV-3", "suppress", 0, []string{"UAV-3"}},
		} {
			rules, unknown := parseDirectiveDetailed(tc.text, edge)
			if len(rules) != 1 || rules[0].Type != tc.action || !reflect.DeepEqual(rules[0].Classes, []string{"marine mammal"}) || rules[0].WithinMin != tc.within || len(unknown) != 0 || (len(tc.sources) > 0 && !reflect.DeepEqual(rules[0].Sources, tc.sources)) {
				t.Errorf("edge=%v %q: rules=%+v unknown=%v", edge, tc.text, rules, unknown)
			}
		}
	}
}

func TestOpenObservationCompilePaths(t *testing.T) {
	for _, edge := range []bool{false, true} {
		compiler := &Compiler{EdgeSubtypes: edge}
		for _, tc := range []struct {
			text, action string
			within       int
			sources      []string
		}{
			{"prioritize whales near the ship", "boost", 0, nil},
			{"ignore whales near the ship", "suppress", 0, nil},
			{"prioritize whales near the ship last 30 minutes", "boost", 30, nil},
			{"ignore whales near the ship from UAV-3", "suppress", 0, []string{"UAV-3"}},
			{"prioritize whales", "boost", 0, nil},
			{"prioritize whales near the cargo ship last 30 minutes", "boost", 30, nil},
		} {
			for _, got := range []Compiled{compiler.Compile(context.Background(), tc.text), compiler.CompileRules(tc.text)} {
				if got.Blocked || len(got.Rules) != 1 || len(got.Unknown) != 0 {
					t.Fatalf("edge=%v %q: %+v", edge, tc.text, got)
				}
				rule := got.Rules[0]
				if rule.Type != tc.action || !reflect.DeepEqual(rule.Classes, []string{"marine mammal"}) || rule.WithinMin != tc.within || !reflect.DeepEqual(rule.Sources, append([]string{}, tc.sources...)) {
					t.Errorf("edge=%v %q: %+v", edge, tc.text, got)
				}
			}
		}
	}
}
