package ingest

import (
	"context"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// tomerStub mimics the sidecar in --prioritizer tomer: no detections, an
// ImagePriority per frame, frame 2 and 3 near-duplicates of frame 1.
type tomerStub struct{ calls int }

func (s *tomerStub) Analyze(_ context.Context, fr inference.Frame, _ []string, _ map[string]string) (*pb.AnalyzeResponse, error) {
	s.calls++
	ip := &pb.ImagePriority{Engine: "ghost_priority/test", FrameId: fr.Source + "#" + string(rune('0'+s.calls)), BaseScore: 0.6, Score: 0.6}
	if s.calls == 2 || s.calls == 3 {
		ip.NearDuplicate, ip.NearestPriorId, ip.MaxSimilarity, ip.Score = true, fr.Source+"#1", 0.97, 0.6*(1-0.85*0.97)
	}
	return &pb.AnalyzeResponse{ModelId: "none", ImagePriority: ip}, nil
}

func (s *tomerStub) EmbedText(context.Context, []string) ([][]float32, error) { return nil, nil }

func TestTomerNearDuplicatesFoldUnderHashDedup(t *testing.T) {
	ctx := context.Background()
	g, st, clk := newIngester(t, DedupHash, &tomerStub{})
	g.cfg.Prioritizer = scoring.PrioritizerTomer
	first, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil || first == nil {
		t.Fatalf("first: %v %v", first, err)
	}
	tags := first.Item.Features.Tags
	if tags[scoring.TagTomerScore] != "0.600" || tags[scoring.TagTomerDup] != "" {
		t.Fatalf("tags: %v", tags)
	}
	for i := 2; i <= 3; i++ { // distinct pixels (SHA-256 differs) but flagged near-duplicate -> folded onto item 1
		row, err := g.Ingest(ctx, capture(clk, uint8(i*40)))
		if err != nil || row == nil || row.Item.Id != first.Item.Id {
			t.Fatalf("frame %d: row=%v err=%v", i, row, err)
		}
	}
	fourth, err := g.Ingest(ctx, capture(clk, 200))
	if err != nil || fourth == nil || fourth.Item.Id == first.Item.Id {
		t.Fatalf("fourth: %v %v", fourth, err)
	}
	if n := len(items(t, st)); n != 2 || g.Deduped() != 2 {
		t.Fatalf("items=%d dups=%d, want 2/2", n, g.Deduped())
	}
}

func TestTomerFoldRebindsAfterReferenceEvicted(t *testing.T) {
	ctx := context.Background()
	g, _, clk := newIngester(t, DedupHash, &tomerStub{})
	g.cfg.Prioritizer = scoring.PrioritizerTomer
	first, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil || first == nil {
		t.Fatalf("first: %v %v", first, err)
	}
	// the reference falls out of the frame->item ring (same path as an evicted item: lookup fails)
	delete(g.tomerItems, "cam-a#1")
	// frame 2 is flagged as a duplicate of frame 1, whose item is gone -> stored as a new item
	second, err := g.Ingest(ctx, capture(clk, 80))
	if err != nil || second == nil || second.Item.Id == first.Item.Id {
		t.Fatalf("second: %v %v", second, err)
	}
	// frame 3 still names frame 1 -> folds onto the item that replaced it
	third, err := g.Ingest(ctx, capture(clk, 120))
	if err != nil || third == nil || third.Item.Id != second.Item.Id {
		t.Fatalf("third: %v %v (want fold onto %s)", third, err, second.Item.Id)
	}
	if g.Deduped() != 1 {
		t.Fatalf("dups=%d, want 1", g.Deduped())
	}
	// #1, #2 and the rebound alias all ride the ring, so the map can never outgrow it
	if g.tomerItems["cam-a#1"] != second.Item.Id || len(g.tomerRing) != 3 || len(g.tomerItems) > len(g.tomerRing) {
		t.Fatalf("alias=%q map=%d ring=%d", g.tomerItems["cam-a#1"], len(g.tomerItems), len(g.tomerRing))
	}
}

func TestTomerNearDuplicatesStayUnderNaive(t *testing.T) {
	ctx := context.Background()
	g, st, clk := newIngester(t, DedupNaive, &tomerStub{})
	g.cfg.Prioritizer = scoring.PrioritizerTomer
	first, _ := g.Ingest(ctx, capture(clk, 1))
	dup, err := g.Ingest(ctx, capture(clk, 80))
	if err != nil || dup == nil || dup.Item.Id == first.Item.Id {
		t.Fatalf("naive keeps the frame: %v %v", dup, err)
	}
	tags := dup.Item.Features.Tags
	if tags[scoring.TagTomerDup] != "1" || tags[scoring.TagTomerNear] != first.Item.Id || tags[scoring.TagTomerScore] >= tags[scoring.TagTomerBase] {
		t.Fatalf("soft suppression tags: %v", tags)
	}
	if len(items(t, st)) != 2 || g.Deduped() != 0 {
		t.Fatalf("items=%d dups=%d", len(items(t, st)), g.Deduped())
	}
}

func TestEdgemlPrioritizerIgnoresNearDuplicateFlag(t *testing.T) {
	ctx := context.Background()
	g, _, clk := newIngester(t, DedupHash, &tomerStub{})
	first, _ := g.Ingest(ctx, capture(clk, 1))
	row, err := g.Ingest(ctx, capture(clk, 80))
	if err != nil || row == nil || row.Item.Id == first.Item.Id {
		t.Fatalf("edgeml + hash must stay exact-hash only: %v %v", row, err)
	}
}
