package scoring

import (
	"fmt"
	"strconv"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// Prioritizer names the scoring backend that drives Score.Priority.
const (
	PrioritizerEdgeml = "edgeml" // Default(): base value, unknownness, interest, confidence
	PrioritizerTomer  = "tomer"  // Tomer(): algorithm/ghost_priority image-level score
)

// Feature tags carrying the sidecar's ImagePriority (set by edge/ingest for
// every response that has one, whichever prioritizer is active, so the shore
// and UI can explain the ranking).
const (
	TagTomerScore = "tomer_score" // final score = base × (1 − duplicate_strength × max_similarity)
	TagTomerBase  = "tomer_base"
	TagTomerSim   = "tomer_sim"  // max combined color+pHash similarity to a prior kept frame
	TagTomerDup   = "tomer_dup"  // "1" when the sidecar flagged a near duplicate
	TagTomerNear  = "tomer_near" // item id of that prior frame, when known
)

// ForPrioritizer returns the scoring engine for a -prioritizer flag value.
func ForPrioritizer(name string) (*Engine, error) {
	switch name {
	case "", PrioritizerEdgeml:
		return Default(), nil
	case PrioritizerTomer:
		return Tomer(), nil
	}
	return nil, fmt.Errorf("unknown prioritizer %q (edgeml | tomer)", name)
}

// Tomer scores image items by Tomer's whole-frame priority alone; operator
// interest, freshness and shore pull boosts (platform features) still apply.
// The weight puts his [0,1] score on the same scale as Default()'s image
// priorities so policy thresholds (min_priority_for_images) keep working.
func Tomer() *Engine {
	return &Engine{
		Scorers: []Scorer{TomerScore{}, Interest{}},
		Weights: map[string]float64{"tomer": 3, "interest": 1},
	}
}

// TomerScore reads the ImagePriority tags written by edge/ingest. Non-image
// items fall back to BaseValue so records and digests still lead.
type TomerScore struct{}

func (TomerScore) Name() string { return "tomer" }

func (TomerScore) Score(it *pb.Item, ctx *Context) Result {
	if it.Type != pb.ItemType_ITEM_TYPE_IMAGE {
		return BaseValue{}.Score(it, ctx)
	}
	tags := it.GetFeatures().GetTags()
	v, err := strconv.ParseFloat(tags[TagTomerScore], 64)
	if err != nil {
		return Result{Value: 0.05, Note: "no image_priority from sidecar (run it with --prioritizer tomer|both)"}
	}
	note := "ghost_priority"
	if tags[TagTomerDup] == "1" {
		note = "ghost_priority near-duplicate of " + tags[TagTomerNear]
	} else if s, err := strconv.ParseFloat(tags[TagTomerSim], 64); err == nil && s > 0 {
		note = fmt.Sprintf("ghost_priority sim=%.2f", s)
	}
	return Result{Value: v, Note: note}
}
