package command

import (
	"reflect"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestReadback(t *testing.T) {
	rules := []Rule{
		{Type: "boost", Classes: []string{"cargo ship", "tanker"}},
		{Type: "suppress", Classes: []string{"bird"}},
	}
	if got := readback(rules, 120); got != "Boost: cargo ship, tanker. Suppress: bird. For 2h." {
		t.Fatalf("readback = %q", got)
	}
	rules = []Rule{
		{Type: "boost", Classes: []string{"drone"}, Attrs: []string{"dark", "moving"}, Only: true, Sources: []string{"EO-P7"}, WithinMin: 30},
		{Type: "suppress", Classes: []string{"bird"}},
	}
	want := "Boost: drone (no AIS, moving fast) from EO-P7 in last 30 min. Focus: other contacts ×0.3. Suppress: bird. For 3d."
	if got := readback(rules, 4320); got != want {
		t.Fatalf("readback = %q, want %q", got, want)
	}
	if got := readback(nil, 0); got != "No rules recognized." {
		t.Fatalf("empty readback = %q", got)
	}
	if got := readback([]Rule{{Type: "boost"}}, 0); got != "Boost: any object. Until cleared." {
		t.Fatalf("open-ended readback = %q", got)
	}
}

func TestGuardrailsEverything(t *testing.T) {
	for _, text := range []string{"prioritize everything"} {
		if got := (&Compiler{}).CompileRules(text); !got.Blocked {
			t.Errorf("%q should be blocked", text)
		}
	}
	for _, text := range []string{
		"submarine periscope, drop everything",
		"keep an eye out for uavs, flash, all units",
		"ignore everything except drones",
		"ships and aircraft",
	} {
		if got := (&Compiler{}).CompileRules(text); got.Blocked {
			t.Errorf("%q should not be blocked: %v", text, got.Warnings)
		}
	}
	classes := make([]string, 0, 12)
	for class := range catalog.Classes {
		classes = append(classes, class)
		if len(classes) == 12 {
			break
		}
	}
	warnings, blocked := guardrails("", []Rule{{Type: "boost", Classes: classes}}, nil, nil)
	if !blocked || len(warnings) != 1 {
		t.Fatalf("12-class guardrail = %v, %t", warnings, blocked)
	}
}

type testCapabilities struct {
	value Capabilities
}

func (c testCapabilities) Capabilities() Capabilities { return c.value }

type testLocks struct {
	warnings []string
	blocked  bool
}

func (l testLocks) CheckLocks([]Rule) ([]string, bool) { return l.warnings, l.blocked }

func TestGuardrailCapabilitiesAndLocks(t *testing.T) {
	rules := []Rule{{Type: "boost", Classes: []string{"tanker"}}}
	warnings, blocked := guardrails("", rules, testCapabilities{Capabilities{FixedLabels: []string{"small boat", "person"}}}, nil)
	if blocked || !reflect.DeepEqual(warnings, []string{"Cannot detect tanker: the analyzer only knows small boat, person."}) {
		t.Fatalf("capability lint = %v, blocked=%t", warnings, blocked)
	}
	compiled := (&Compiler{Locks: testLocks{warnings: []string{"Locked."}, blocked: true}}).Review("", rules, 0)
	if !compiled.Blocked || !reflect.DeepEqual(compiled.Warnings, []string{"Locked."}) {
		t.Fatalf("lock result = %+v", compiled)
	}
}
