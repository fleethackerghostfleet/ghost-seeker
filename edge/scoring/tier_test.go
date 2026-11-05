package scoring

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestPinnedTierInterest(t *testing.T) {
	for _, engine := range []*Engine{Default(), Tomer()} {
		item := img("tanker", .9, 0)
		ctx := &Context{Now: time.Now(), Policy: &pb.Policy{Interest: &pb.Interest{}}}
		base := engine.Score(item, ctx, 0).Priority
		for tier := int32(1); tier <= 4; tier++ {
			ctx.Policy.Interest.TierLabels = map[string]int32{"tanker": tier}
			got := engine.Score(item, ctx, 0)
			if tier == 1 && got.Priority <= base || (tier == 2 || tier == 3) && got.Priority != base || tier == 4 && (got.Priority >= base || !got.Suppressed) {
				t.Fatalf("P%d: base=%v got=%v", tier, base, got)
			}
		}
		ctx.Policy.Interest.TierLabels = map[string]int32{"other": 1, "tanker": 5}
		if got := engine.Score(item, ctx, 0); got.Priority != base || got.Suppressed {
			t.Fatalf("nonmatching/invalid pin changed score: %v", got)
		}
		ctx.Policy.Interest = &pb.Interest{TierLabels: map[string]int32{"tanker": 2}, SuppressLabels: []string{"tanker"}}
		if got := engine.Score(item, ctx, 0); got.Priority != base || got.Suppressed {
			t.Fatalf("pin must override generic suppression: %v", got)
		}
	}
}
