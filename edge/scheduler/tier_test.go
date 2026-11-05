package scheduler

import (
	"context"
	"testing"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestPinnedPolicyRescoresSchedulerOrder(t *testing.T) {
	s, st, _, clk := setup(t, Prioritized)
	for _, label := range []string{"critical", "high", "routine", "low"} {
		addImage(t, s, st, clk, label, .9, 0, 30000)
	}
	ctx := context.Background()
	if _, _, err := s.ApplyPolicy(ctx, &pb.Policy{Id: "pins", Interest: &pb.Interest{TierLabels: map[string]int32{"critical": 1, "high": 2, "routine": 3, "low": 4}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scores := map[string]float64{}
	for _, r := range rows {
		label := r.Item.Features.Detections[0].Label
		scores[label] = r.Score.Priority
		if label == "low" && !r.Score.Suppressed {
			t.Fatal("P4 pin must suppress payloads")
		}
	}
	if !(scores["critical"] > scores["high"] && scores["high"] == scores["routine"] && scores["routine"] > scores["low"]) {
		t.Fatalf("unexpected score-based order: %v", scores)
	}
}
