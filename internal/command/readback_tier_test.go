package command

import (
	"strings"
	"testing"
)

func TestTargetTierReadback(t *testing.T) {
	for tier, label := range map[int]string{1: "P1 Critical", 2: "P2 High", 3: "P3 Routine", 4: "P4 Low"} {
		for _, kind := range []string{"boost", "suppress"} {
			rule := Rule{Type: kind, Classes: []string{"tanker"}, Tier: tier}
			got := (&Compiler{}).Review("feedback", []Rule{rule}, 0).Readback
			if got != "Set tanker → "+label+". Until cleared." {
				t.Fatalf("target readback: %q", got)
			}
		}
	}
	if got := readback(nil, 0); got != "No rules recognized." {
		t.Fatal(got)
	}
	if got := readback([]Rule{{Type: "suppress", Classes: []string{"tanker"}, Tier: 5}}, 0); strings.Contains(got, "→") {
		t.Fatalf("invalid tier rendered as a target: %q", got)
	}
}
