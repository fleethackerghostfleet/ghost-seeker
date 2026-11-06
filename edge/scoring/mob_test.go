package scoring

import (
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestMOBHasCriticalValueAcrossPrioritizers(t *testing.T) {
	ctx := &Context{Now: time.Now()}
	for _, engine := range []*Engine{Default(), Tomer()} {
		item := img("boat", .99, 0)
		base := engine.Score(item, ctx, 0).Priority
		item.Features.Detections = append(item.Features.Detections, &pb.Detection{Label: "person in water", Confidence: .35})
		critical := engine.Score(item, ctx, 0).Priority
		if critical < base+3.9 {
			t.Fatalf("MOB lost behind higher-confidence boat: base=%v critical=%v", base, critical)
		}
		item.Features.LabelOverride = "boat"
		if got := (PersonInWater{}).Score(item, ctx); got.Value != 0 {
			t.Fatal("human correction must clear MOB boost")
		}
		item.Features.LabelOverride = ""
		item.Features.Detections[1].Confidence = .1
		if got := (PersonInWater{}).Score(item, ctx); got.Value != 0 {
			t.Fatal("low-confidence person must not boost")
		}
	}
}
