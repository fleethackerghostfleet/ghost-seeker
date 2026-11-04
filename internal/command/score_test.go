package command

import (
	"math"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestRescoreWithoutRules(t *testing.T) {
	item := Item{Dets: []Detection{{Class: "warship", Conf: .75}}}
	Rescore(&item, "maritime", nil)
	if item.Base != item.Central {
		t.Fatalf("base %v != central %v without rules", item.Base, item.Central)
	}
}

func TestSuppressRuleLowersMatchingScore(t *testing.T) {
	detection := Detection{Class: "warship", Conf: .9}
	base := ScoreDet(detection, nil, "maritime").Score
	suppressed := ScoreDet(detection, []Rule{{Type: "suppress", Classes: []string{"warship"}}}, "maritime").Score
	if suppressed >= base {
		t.Fatalf("suppressed score %v is not lower than base score %v", suppressed, base)
	}
}

func TestConditionalBoostMatchesMockupFormula(t *testing.T) {
	detection := Detection{
		Class: "small boat",
		Conf:  .90,
		Attrs: Attributes{
			Approaching: true,
			Moving:      true,
		},
	}
	directive := Directive{Rules: ParseDirective("Small boats approaching the pier at speed")}
	item := Item{Dets: []Detection{detection}}
	Rescore(&item, "maritime", []Directive{directive})

	model := catalog.Models["maritime"]
	weight := 1 - (1-model.W[detection.Class])*(1-model.Lift["approaching"])
	expected := min(.99, (1-(1-weight)*.06)*ConfFactor(detection.Conf))
	if math.Abs(item.Central-expected) > 1e-12 {
		t.Fatalf("central score = %.6f, want %.6f", item.Central, expected)
	}
	if item.Tier != 1 {
		t.Fatalf("tier = %d, want 1 for score %.6f", item.Tier, item.Central)
	}
}

func TestManualTierOverridesScore(t *testing.T) {
	item := Item{Manual: 1, Dets: []Detection{{Class: "bird", Conf: .1}}}
	Rescore(&item, "maritime", nil)
	if item.Tier != 1 {
		t.Fatalf("tier = %d, want manual tier 1", item.Tier)
	}
}

func TestTierForThresholds(t *testing.T) {
	tests := []struct {
		score float64
		tier  int
	}{
		{.8, 1},
		{.6, 2},
		{.35, 3},
		{math.Nextafter(.8, 0), 2},
		{math.Nextafter(.6, 0), 3},
		{math.Nextafter(.35, 0), 4},
	}
	for _, test := range tests {
		if got := TierFor(test.score); got != test.tier {
			t.Errorf("TierFor(%v) = %d, want %d", test.score, got, test.tier)
		}
	}
}

func TestFrameScoreDampsCrowds(t *testing.T) {
	if got := FrameScore([]float64{.3, .3, .3, .3, .3}); got >= .4 {
		t.Fatalf("five minor contacts scored %.3f, want to stay near the top contact (< .4)", got)
	}
	if got := FrameScore([]float64{.9}); got != .9 {
		t.Fatalf("single contact = %.3f, want .9", got)
	}
	if FrameScore(nil) != 0 {
		t.Fatal("empty frame should score 0")
	}
}

func TestLiftsSkipClutter(t *testing.T) {
	bird := Detection{Class: "bird", Attrs: Attributes{Approaching: true, Dark: true}}
	if got := ContactWeight(bird, "maritime"); got != catalog.Models["maritime"].W["bird"] {
		t.Fatalf("approaching bird weight = %.3f, want unlifted", got)
	}
}
