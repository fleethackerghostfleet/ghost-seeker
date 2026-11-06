package command

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
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
			if len(got.Rules) != 1 || !reflect.DeepEqual(got.Rules[0].Classes, []string{"marine mammal"}) || len(got.Unknown) != 0 {
				t.Fatalf("edge=%v mammal mapping %q: %+v", edge, text, got)
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
	if len(got.Rules) != 0 || !strings.HasPrefix(got.Readback, `Not supported: "unicorns" — this sensor can detect: `) {
		t.Fatalf("unsupported readback: %+v", got)
	}
	labels := strings.TrimSuffix(strings.Split(got.Readback, "this sensor can detect: ")[1], ".")
	for _, label := range strings.Split(labels, ", ") {
		if _, ok := catalog.Classes[label]; !ok {
			t.Errorf("invented simulation class %q", label)
		}
	}
	if len(strings.Split(labels, ", ")) != len(catalog.Classes) {
		t.Fatalf("incomplete supported list: %s", labels)
	}
	mixed := compiler.CompileRules("prioritize sailboats and unicorns")
	if len(mixed.Rules) != 1 || !strings.HasPrefix(mixed.Readback, "Boost: small boat. Until cleared. Not supported:") {
		t.Fatalf("partial interpretation lost: %+v", mixed)
	}
	if empty := compiler.CompileRules(""); empty.Readback != "No rules recognized." || len(empty.Unknown) != 0 {
		t.Fatalf("empty interpretation: %+v", empty)
	}
	compiler.Capabilities = testCapabilities{value: Capabilities{FixedLabels: []string{"person", "small boat"}}}
	if got := compiler.CompileRules("report unicorns"); got.Readback != `Not supported: "unicorns" — this sensor can detect: person, small boat.` {
		t.Fatalf("fixed sensor labels ignored: %+v", got)
	}
	live := (&Compiler{EdgeSubtypes: true}).CompileRules("report unicorns")
	if !strings.Contains(live.Readback, "marine mammal") || !strings.Contains(live.Readback, "sailboat") {
		t.Fatalf("live sensor classes missing: %+v", live)
	}
}
