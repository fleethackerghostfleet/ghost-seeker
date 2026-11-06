package ingest

import (
	"context"
	"slices"
	"testing"
	"time"

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

func TestOpenVocabularyKeepsSightingLifecycle(t *testing.T) {
	an := &scripted{script: map[int][]*pb.Sighting{
		1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "boat")},
		2: {sighting("s1", pb.Sighting_EVENT_UPDATE, false, "tanker")},
		3: {sighting("s1", pb.Sighting_EVENT_CLOSE, false, "tanker")},
	}, detections: map[int][]*pb.Detection{}}
	for i := 1; i <= 3; i++ {
		an.detections[i] = []*pb.Detection{{Label: "life raft", Confidence: .7}}
	}
	g, st, clk := newIngester(t, DedupSightings, an)
	ctx := context.Background()
	_, _, _ = g.sch.ApplyPolicy(ctx, &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"life raft"}}})
	if _, err := g.Ingest(ctx, capture(clk, 10)); err != nil {
		t.Fatal(err)
	}
	id := g.sightings["s1"]
	if id == "" {
		t.Fatal("open sighting lost on matching frame")
	}
	if _, err := g.Ingest(ctx, capture(clk, 11)); err != nil {
		t.Fatal(err)
	}
	tracked, _ := st.Get(ctx, id)
	if tracked.Item.Features.Tags["subtype"] != "tanker" {
		t.Fatal("sighting update lost")
	}
	if _, err := g.Ingest(ctx, capture(clk, 12)); err != nil {
		t.Fatal(err)
	}
	if g.sightings["s1"] != "" {
		t.Fatal("sighting close lost")
	}
	for _, row := range items(t, st) {
		if row.Item.Type == pb.ItemType_ITEM_TYPE_RECORD && row.Item.Features.Tags["event"] == "close" {
			return
		}
	}
	t.Fatal("close contact report lost")
}

func TestOpenVocabularyDoesNotFoldIntoOldSceneOrDifferentMatch(t *testing.T) {
	an := &scripted{empty: map[int]bool{1: true}, detections: map[int][]*pb.Detection{
		2: {{Label: "life raft", Confidence: .7}},
		3: {{Label: "life raft", Confidence: .7}, {Label: "oil slick", Confidence: .7}},
		4: {{Label: "life raft", Confidence: .7}, {Label: "oil slick", Confidence: .7}},
	}}
	g, _, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Minute
	ctx := context.Background()
	scene, err := g.Ingest(ctx, capture(clk, 10))
	if err != nil || scene == nil || scene.Item.Features.Tags["event"] != "scene" {
		t.Fatalf("scene setup: %v %v", scene, err)
	}
	_, _, _ = g.sch.ApplyPolicy(ctx, &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"life raft", "oil slick"}}})
	first, err := g.Ingest(ctx, capture(clk, 10))
	if err != nil || first == nil || first.Item.Id == scene.Item.Id || first.Item.Features.Detections[0].Label != "life raft" {
		t.Fatalf("scene swallowed new match: %v %v", first, err)
	}
	second, err := g.Ingest(ctx, capture(clk, 10))
	if err != nil || second == nil || second.Item.Id == first.Item.Id || len(second.Item.Features.Detections) != 2 {
		t.Fatalf("old match swallowed new label: %v %v", second, err)
	}
	duplicate, err := g.Ingest(ctx, capture(clk, 10))
	if err != nil || duplicate == nil || duplicate.Item.Id != second.Item.Id {
		t.Fatalf("same matches failed to deduplicate: %v %v", duplicate, err)
	}
}
