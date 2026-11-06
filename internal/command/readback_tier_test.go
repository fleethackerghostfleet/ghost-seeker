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
	if got := readback(nil, 0); got != NoMatchReadback {
		t.Fatal(got)
	}
	if got := readback([]Rule{{Type: "suppress", Classes: []string{"tanker"}, Tier: 5}}, 0); strings.Contains(got, "→") {
		t.Fatalf("invalid tier rendered as a target: %q", got)
	}
}

func TestReadbackPreservesApplicationOrder(t *testing.T) {
	target := Rule{Type: "suppress", Classes: []string{"tanker"}, Tier: 2}
	suppress := Rule{Type: "suppress", Classes: []string{"tanker"}}
	boost := Rule{Type: "boost", Classes: []string{"tanker"}}
	for _, tc := range []struct {
		rules []Rule
		want  string
		tier  int
	}{
		{[]Rule{suppress, target}, "Suppress: tanker. Set tanker → P2 High. Until cleared.", 2},
		{[]Rule{target, suppress}, "Set tanker → P2 High. Suppress: tanker. Until cleared.", 4},
		{[]Rule{boost, target}, "Boost: tanker. Set tanker → P2 High. Until cleared.", 2},
		{[]Rule{target, boost}, "Set tanker → P2 High. Boost: tanker. Until cleared.", 1},
	} {
		if got := readback(tc.rules, 0); got != tc.want {
			t.Errorf("readback = %q, want %q", got, tc.want)
		}
		if got := TierFor(ScoreDet(Detection{Class: "tanker", Conf: .9}, tc.rules, "maritime").Score); got != tc.tier {
			t.Errorf("effective tier = %d, want %d", got, tc.tier)
		}
	}
	if got := readback([]Rule{suppress, boost}, 0); got != "Suppress: tanker. Boost: tanker. Until cleared." {
		t.Fatalf("generic rules reordered: %q", got)
	}
}
