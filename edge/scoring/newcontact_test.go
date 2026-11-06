package scoring

import (
	"math"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestNewContactBoost(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	ctx := &Context{Now: now}
	if r := (NewContact{}).Score(nil, ctx); r.Value != 0 || r.Note != "" {
		t.Fatalf("empty: %+v", r)
	}
	for _, engine := range []*Engine{Default(), Tomer()} {
		it := img("boat", .8, 0)
		it.CapturedAtUnixMs = now.UnixMilli()
		it.Features.Tags = map[string]string{"new_contact": "0"}
		before := engine.Score(it, ctx, 0)
		it.Features.Tags["new_contact"] = "1"
		after := engine.Score(it, ctx, 0)
		if math.Abs(after.Priority-before.Priority-1) > 1e-9 {
			t.Fatalf("new contact boost: %v -> %v", before.Priority, after.Priority)
		}
		found := false
		for _, reason := range after.Reasons {
			if reason.Scorer == "new_contact" {
				found = reason.Value == 1 && reason.Note != ""
			}
		}
		if !found {
			t.Fatal("missing new_contact reason")
		}
	}
	if Default().Weights["new_contact"] >= Default().Weights["unknown"] {
		t.Fatal("unknown must retain the larger weight")
	}
	ctx.Policy = &pb.Policy{ScorerWeights: map[string]float64{"new_contact": 0}}
	it := img("boat", .8, 0)
	it.CapturedAtUnixMs = now.UnixMilli()
	it.Features.Tags = map[string]string{}
	before := Default().Score(it, ctx, 0)
	it.Features.Tags["new_contact"] = "1"
	if after := Default().Score(it, ctx, 0); after.Priority != before.Priority {
		t.Fatal("zero policy weight must disable new-contact boost")
	}
}
