package ingest

import (
	"context"
	"slices"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestPolicyOpenVocabularyReachesAnalyzeAndSurvivesTrackerWarmup(t *testing.T) {
	an := &scripted{detections: map[int][]*pb.Detection{1: {{Label: "life raft", Confidence: .7}}, 2: {{Label: "life raft", Confidence: .7}}}}
	g, _, clk := newIngester(t, DedupSightings, an)
	ctx := context.Background()
	if _, _, err := g.sch.ApplyPolicy(ctx, &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"life raft"}}}); err != nil {
		t.Fatal(err)
	}
	row, err := g.Ingest(ctx, capture(clk, 4))
	if err != nil || row == nil {
		t.Fatalf("open match folded during warmup: %v %v", row, err)
	}
	if !slices.Contains(an.labels[0], "open-vocab:life raft") {
		t.Fatalf("missing labels: %v", an.labels)
	}
	interest := (scoring.Interest{}).Score(row.Item, &scoring.Context{Policy: g.sch.Policy()})
	if interest.Value != 1 || interest.Multiplier <= 1 {
		t.Fatalf("open label did not receive directive boost: %+v", interest)
	}
	if _, _, err := g.sch.ApplyPolicy(ctx, &pb.Policy{}); err != nil {
		t.Fatal(err)
	}
	_, _ = g.Ingest(ctx, capture(clk, 5))
	if slices.Contains(an.labels[1], "open-vocab:life raft") {
		t.Fatal("removed policy prompt leaked")
	}
}
