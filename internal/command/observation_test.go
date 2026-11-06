package command

import (
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
	compiler := &Compiler{EdgeSubtypes: true}
	for _, tc := range []struct{ text, action string }{
		{"prioritize whale near the ship", "boost"},
		{"ignore whales near the ship", "suppress"},
	} {
		got := compiler.CompileRules(tc.text)
		if len(got.Rules) != 1 || got.Rules[0].Type != tc.action || !reflect.DeepEqual(got.Rules[0].Classes, []string{"marine mammal"}) || len(got.Unknown) != 0 {
			t.Errorf("%q = %+v; want %s marine mammal", tc.text, got, tc.action)
		}
	}
}
