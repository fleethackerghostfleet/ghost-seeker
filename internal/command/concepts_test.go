package command

import (
	"context"
	"reflect"
	"testing"
)

func TestOfflineConceptMapping(t *testing.T) {
	for _, label := range []string{"", " ", "red whale"} {
		if got, ok := canonicalClass(label, true); ok {
			t.Fatalf("unsupported class %q became %q", label, got)
		}
	}
	for _, edge := range []bool{false, true} {
		compiler := &Compiler{EdgeSubtypes: edge}
		for _, text := range []string{"report sailboats", "prioritize sailing boats", "prioritize sailing yachts", "report catamarans"} {
			want := "small boat"
			if edge {
				want = "sailboat"
			}
			for _, got := range []Compiled{compiler.CompileRules(text), compiler.Compile(context.Background(), text)} {
				if got.Blocked || got.Path != "rules" || len(got.Rules) != 1 || !reflect.DeepEqual(got.Rules[0].Classes, []string{want}) || len(got.Unknown) != 0 {
					t.Fatalf("edge=%v %q: %+v", edge, text, got)
				}
				if got.Readback != "Boost: "+want+". Until cleared." {
					t.Fatalf("readback = %q", got.Readback)
				}
			}
		}
		for _, text := range []string{"prioritize whales", "report dolphins", "report marine mammals", "prioritize orcas"} {
			got := compiler.CompileRules(text)
			if edge {
				if len(got.Rules) != 1 || !reflect.DeepEqual(got.Rules[0].Classes, []string{"marine mammal"}) || len(got.Unknown) != 0 {
					t.Fatalf("live mammal mapping %q: %+v", text, got)
				}
			} else if len(got.Rules) != 0 || got.Readback != NoMatchReadback || len(got.Unknown) == 0 {
				t.Fatalf("unsupported simulation mammal %q: %+v", text, got)
			}
		}
	}
	got := (&Compiler{}).CompileRules("ignore sailboats")
	if len(got.Rules) != 1 || got.Rules[0].Type != "suppress" {
		t.Fatalf("negative action lost: %+v", got)
	}
}

func TestUnsupportedReadback(t *testing.T) {
	compiler := &Compiler{}
	got := compiler.CompileRules("prioritize unicorns")
	if len(got.Rules) != 0 || got.Readback != NoMatchReadback || !reflect.DeepEqual(got.Unknown, []string{"unicorns"}) {
		t.Fatalf("unsupported readback: %+v", got)
	}
	mixed := compiler.CompileRules("prioritize sailboats and unicorns")
	if len(mixed.Rules) != 1 || mixed.Readback != "Boost: small boat. Until cleared." || len(mixed.Unknown) == 0 {
		t.Fatalf("partial interpretation lost: %+v", mixed)
	}
	if empty := compiler.CompileRules(""); empty.Readback != NoMatchReadback || len(empty.Unknown) != 0 {
		t.Fatalf("empty interpretation: %+v", empty)
	}
	compiler.Capabilities = testCapabilities{value: Capabilities{FixedLabels: []string{"person", "small boat"}}}
	live := (&Compiler{EdgeSubtypes: true}).CompileRules("report unicorns")
	for _, got := range []Compiled{compiler.CompileRules("report unicorns"), live} {
		if got.Readback != NoMatchReadback {
			t.Fatalf("vocabulary listed in readback: %+v", got)
		}
	}
}
