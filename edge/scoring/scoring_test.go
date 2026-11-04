package scoring

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func img(label string, conf, unk float32) *pb.Item {
	it := &pb.Item{Type: pb.ItemType_ITEM_TYPE_IMAGE, CapturedAtUnixMs: time.Now().UnixMilli(), Features: &pb.Features{Unknownness: unk}}
	if label != "" {
		it.Features.Detections = []*pb.Detection{{Label: label, Confidence: conf}}
	}
	return it
}

func TestUnknownOutranksKnown(t *testing.T) {
	e := Default()
	ctx := &Context{Now: time.Now()}
	u := e.Score(img("", 0, 0.9), ctx, 0).Priority
	k := e.Score(img("cargo ship", 0.95, 0.05), ctx, 0).Priority
	o := e.Score(img("", 0, 0.1), ctx, 0).Priority
	if !(u > k && k > o) {
		t.Fatalf("want unknown %v > known %v > empty %v", u, k, o)
	}
}

func TestOverrideResolvesUnknown(t *testing.T) {
	e := Default()
	ctx := &Context{Now: time.Now()}
	it := img("", 0, 0.9)
	before := e.Score(it, ctx, 0).Priority
	it.Features.LabelOverride, it.Features.OverrideBy = "dolphin", "ops"
	after := e.Score(it, ctx, 0).Priority
	if after >= before {
		t.Fatalf("override should lower unknown priority: %v -> %v", before, after)
	}
}

func TestInterestBoostAndSuppress(t *testing.T) {
	e := Default()
	p := &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"container"}, SuppressLabels: []string{"sailboat"}}}
	ctx := &Context{Policy: p, Now: time.Now()}
	base := e.Score(img("tanker", 0.9, 0), ctx, 0).Priority
	boost := e.Score(img("container ship", 0.9, 0), ctx, 0)
	sup := e.Score(img("sailboats", 0.9, 0), ctx, 0)
	if boost.Priority <= base {
		t.Fatalf("boost %v <= base %v", boost.Priority, base)
	}
	if !sup.Suppressed || sup.Priority >= base || sup.Priority <= 0 {
		t.Fatalf("suppress: %+v base %v", sup, base)
	}
	// An unknown is never suppressed, even if a weak detection matches.
	unk := img("sailboat", 0.3, 0.9)
	if r := e.Score(unk, ctx, 0); r.Suppressed {
		t.Fatal("unknowns must not be suppressed")
	}
}

func TestFreshnessFloor(t *testing.T) {
	it := img("x", 0.9, 0)
	it.CapturedAtUnixMs = time.Now().Add(-100 * time.Hour).UnixMilli()
	if f := Freshness(it, time.Now()); f < 0.25 || f > 0.26 {
		t.Fatalf("freshness floor: %v", f)
	}
}
