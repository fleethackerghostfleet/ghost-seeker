package scoring

import (
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// PersonInWater gives a possible rescue contact critical base value, even when
// a more confident vessel is in the same frame. Operator suppression still wins.
type PersonInWater struct{}

func (PersonInWater) Name() string { return "mob" }
func (PersonInWater) Score(it *pb.Item, ctx *Context) Result {
	f := it.GetFeatures()
	if f == nil {
		return Result{}
	}
	if f.LabelOverride != "" {
		if strings.EqualFold(f.LabelOverride, "person in water") {
			return Result{Value: 1, Note: "possible person in water / MOB"}
		}
		return Result{}
	}
	_, minConf := thresholds(ctx.Policy)
	for _, d := range f.Detections {
		if strings.EqualFold(d.Label, "person in water") && d.Confidence >= minConf {
			return Result{Value: 1, Note: "possible person in water / MOB"}
		}
	}
	return Result{}
}
