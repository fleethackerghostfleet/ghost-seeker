package scoring

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestForPrioritizer(t *testing.T) {
	if e, err := ForPrioritizer("edgeml"); err != nil || len(e.Scorers) != 4 {
		t.Fatalf("edgeml: %v %d", err, len(e.Scorers))
	}
	if e, err := ForPrioritizer("tomer"); err != nil || e.Scorers[0].Name() != "tomer" {
		t.Fatalf("tomer: %v", err)
	}
	if _, err := ForPrioritizer("bogus"); err == nil {
		t.Fatal("bogus accepted")
	}
}

func TestTomerScoreOrdersByTagAndKeepsInterest(t *testing.T) {
	eng := Tomer()
	ctx := &Context{Now: time.Unix(1000, 0), Policy: &pb.Policy{Interest: &pb.Interest{SuppressLabels: []string{"cruise ship"}}}}
	img := func(score, sim string, label string) *pb.Item {
		return &pb.Item{Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: 1000_000,
			Features: &pb.Features{Tags: map[string]string{TagTomerScore: score, TagTomerSim: sim},
				Detections: []*pb.Detection{{Label: label, Confidence: 0.9}}}}
	}
	hi := eng.Score(img("0.60", "0", "submarine"), ctx, 0)
	lo := eng.Score(img("0.10", "0.9", "submarine"), ctx, 0)
	none := eng.Score(&pb.Item{Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: 1000_000, Features: &pb.Features{}}, ctx, 0)
	if !(hi.Priority > lo.Priority && lo.Priority > none.Priority) {
		t.Fatalf("order: hi=%v lo=%v none=%v", hi.Priority, lo.Priority, none.Priority)
	}
	sup := eng.Score(img("0.60", "0", "cruise ship"), ctx, 0)
	if !sup.Suppressed || sup.Priority >= hi.Priority {
		t.Fatalf("operator suppression must still apply: %+v", sup)
	}
	rec := eng.Score(&pb.Item{Type: pb.ItemType_ITEM_TYPE_RECORD, CapturedAtUnixMs: 1000_000}, ctx, 0)
	if rec.Priority <= hi.Priority {
		t.Fatalf("records keep base value: rec=%v hi=%v", rec.Priority, hi.Priority)
	}
}
