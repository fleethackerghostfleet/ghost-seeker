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

func TestInterestMatchesSubtypeTag(t *testing.T) {
	// The sighting pipeline labels the detection "boat" and puts the zero-shot subtype in tags; an operator
	// directive names the subtype. Unknowns stay exempt from suppression, and an override wins over the tag.
	p := &pb.Policy{Interest: &pb.Interest{SuppressLabels: []string{"cargo ship"}, BoostLabels: []string{"submarine"}}}
	ctx := &Context{Policy: p, Now: time.Now()}
	mk := func(subtype string, unk float32) *pb.Item {
		return &pb.Item{Type: pb.ItemType_ITEM_TYPE_IMAGE, Features: &pb.Features{Unknownness: unk,
			Detections: []*pb.Detection{{Label: "boat", Confidence: 0.9}},
			Tags:       map[string]string{"sighting_id": "s1", "subtype": subtype}}}
	}
	if r := (Interest{}).Score(mk("cargo ship", 0), ctx); !r.Suppressed {
		t.Fatalf("subtype cargo ship should be suppressed: %+v", r)
	}
	if r := (Interest{}).Score(mk("submarine", 0), ctx); r.Multiplier <= 1 {
		t.Fatalf("subtype submarine should be boosted: %+v", r)
	}
	if r := (Interest{}).Score(mk("cargo ship", 0.9), ctx); r.Suppressed {
		t.Fatalf("unknown items are exempt from suppression: %+v", r)
	}
	over := mk("cargo ship", 0)
	over.Features.LabelOverride = "tanker"
	if r := (Interest{}).Score(over, ctx); r.Suppressed {
		t.Fatalf("operator override replaces the subtype: %+v", r)
	}
	if r := (Interest{}).Score(mk("", 0), ctx); r.Suppressed || r.Multiplier > 1 {
		t.Fatalf("no subtype, detector label boat: neutral, got %+v", r)
	}
}

func TestDarkContactScoringAndPolicyLabels(t *testing.T) {
	e := Default()
	ctx := &Context{Now: time.Now()}
	dark := img("dark contact", 0.9, 0)
	lit := img("boat", 0.9, 0)
	darkPriority := e.Score(dark, ctx, 0)
	litPriority := e.Score(lit, ctx, 0)
	if darkPriority.Priority <= litPriority.Priority {
		t.Fatalf("dark contact priority %v <= lit boat %v", darkPriority.Priority, litPriority.Priority)
	}
	foundDark := false
	for _, reason := range darkPriority.Reasons {
		if reason.Scorer == "dark" && reason.Note == "dark contact: no navigation lights at night" {
			foundDark = true
		}
	}
	if !foundDark {
		t.Fatalf("dark-contact reason missing: %+v", darkPriority.Reasons)
	}

	policy := &pb.Policy{Interest: &pb.Interest{BoostLabels: []string{"dark vessels"}}}
	boosted := e.Score(dark, &Context{Policy: policy, Now: time.Now()}, 0)
	if boosted.Priority <= darkPriority.Priority {
		t.Fatalf("dark-vessel alias did not boost priority: %v <= %v", boosted.Priority, darkPriority.Priority)
	}
	policy = &pb.Policy{Interest: &pb.Interest{SuppressLabels: []string{"vessels running dark"}}}
	suppressed := e.Score(dark, &Context{Policy: policy, Now: time.Now()}, 0)
	if !suppressed.Suppressed {
		t.Fatalf("dark-vessel alias did not suppress: %+v", suppressed)
	}
	policy = &pb.Policy{ScorerWeights: map[string]float64{"dark": 0}}
	disabled := e.Score(dark, &Context{Policy: policy, Now: time.Now()}, 0)
	for _, reason := range disabled.Reasons {
		if reason.Scorer == "dark" {
			t.Fatalf("disabled dark scorer still emitted a reason: %+v", disabled.Reasons)
		}
	}
}

func TestDarkContactHonorsLabelOverride(t *testing.T) {
	item := img("dark contact", 0.9, 0)
	item.Features.LabelOverride = "boat"
	result := (DarkContact{}).Score(item, &Context{Now: time.Now()})
	if result.Value != 0 || result.Note != "" {
		t.Fatalf("non-dark override received dark score: %+v", result)
	}
	for _, reason := range Default().Score(item, &Context{Now: time.Now()}, 0).Reasons {
		if reason.Scorer == "dark" {
			t.Fatalf("non-dark override received dark reason: %+v", reason)
		}
	}

	item.Features.LabelOverride = "dark vessel"
	result = (DarkContact{}).Score(item, &Context{Now: time.Now()})
	if result.Value != float64(float32(0.9)) || result.Note != "dark contact: no navigation lights at night" {
		t.Fatalf("normalized dark override lost dark score: %+v", result)
	}
}
