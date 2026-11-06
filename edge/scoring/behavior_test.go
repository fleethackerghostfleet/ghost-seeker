package scoring

import (
	"math"
	"strings"
	"testing"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

type fixedScorer struct {
	name   string
	result Result
}

func (s fixedScorer) Name() string                    { return s.name }
func (s fixedScorer) Score(*pb.Item, *Context) Result { return s.result }

func TestEngineCompositionWeightsAndFloor(t *testing.T) {
	now := time.Unix(1700000000, 0)
	it := &pb.Item{CapturedAtUnixMs: now.UnixMilli()}
	ctx := &Context{Now: now, Policy: &pb.Policy{ScorerWeights: map[string]float64{"override": 3, "dark": 0}}}
	e := &Engine{Weights: map[string]float64{"override": 99, "weighted": 2}, Scorers: []Scorer{
		fixedScorer{"override", Result{Value: 2}}, fixedScorer{"weighted", Result{Value: 1}},
		fixedScorer{"fallback", Result{Value: 1, Multiplier: 2, Suppressed: true, Note: "intent"}},
		fixedScorer{"dark", Result{Value: 100, Multiplier: 100, Suppressed: true, Note: "disabled"}},
		fixedScorer{"empty", Result{}},
	}}
	score := e.Score(it, ctx, .5)
	if score.Priority != 18.5 || !score.Suppressed || score.ComputedAtUnixMs != now.UnixMilli() || len(score.Reasons) != 5 {
		t.Fatalf("composition=%+v", score)
	}
	for _, r := range score.Reasons {
		if r.Scorer == "dark" || r.Scorer == "empty" {
			t.Fatalf("disabled reason=%+v", r)
		}
	}
	for _, scorers := range [][]Scorer{nil, {fixedScorer{"negative", Result{Value: -100}}}} {
		s := (&Engine{Scorers: scorers}).Score(it, &Context{Now: now}, 0)
		if s.Priority != .001 || len(s.Reasons) == 0 {
			t.Fatalf("floor=%+v", s)
		}
	}
	it.Features = &pb.Features{Tags: map[string]string{"event": "scene"}}
	scene := e.Score(it, ctx, 2)
	if scene.Priority != 2.0001 || len(scene.Reasons) != 2 || scene.Suppressed {
		t.Fatalf("scene=%+v", scene)
	}
}

func TestFreshnessTypeHalfLivesAndFutureCaptures(t *testing.T) {
	now := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		kind pb.ItemType
		half time.Duration
	}{
		{pb.ItemType_ITEM_TYPE_IMAGE, 2 * time.Hour}, {pb.ItemType_ITEM_TYPE_RECORD, 6 * time.Hour},
		{pb.ItemType_ITEM_TYPE_DIGEST, 6 * time.Hour}, {pb.ItemType_ITEM_TYPE_TELEMETRY, 30 * time.Minute},
	} {
		it := &pb.Item{Type: tc.kind, CapturedAtUnixMs: now.Add(-tc.half).UnixMilli()}
		if got := Freshness(it, now); math.Abs(got-.625) > 1e-10 {
			t.Fatalf("half life %v=%v", tc.kind, got)
		}
		it.CapturedAtUnixMs = now.Add(time.Hour).UnixMilli()
		if Freshness(it, now) != 1 {
			t.Fatal("future capture amplified freshness")
		}
	}
	for _, tc := range []struct {
		kind pb.ItemType
		want float64
	}{
		{pb.ItemType_ITEM_TYPE_RECORD, 1}, {pb.ItemType_ITEM_TYPE_DIGEST, 1},
		{pb.ItemType_ITEM_TYPE_TELEMETRY, .3}, {pb.ItemType_ITEM_TYPE_IMAGE, .2},
	} {
		if got := (BaseValue{}).Score(&pb.Item{Type: tc.kind}, nil); got.Value != tc.want {
			t.Fatalf("base=%+v", got)
		}
	}
}

func TestThresholdedUnknownAndConfidenceOverrides(t *testing.T) {
	ctx := &Context{Policy: &pb.Policy{Thresholds: &pb.Thresholds{UnknownMin: .8, DetectionMinConf: .7}}}
	it := &pb.Item{Features: &pb.Features{Unknownness: .6, Detections: []*pb.Detection{{Label: "boat", Confidence: .6}, {Label: "submarine", Confidence: .75}}}}
	if u := (Unknown{}).Score(it, ctx); math.Abs(u.Value-.12) > 1e-6 || u.Note != "" {
		t.Fatalf("unknown=%+v", u)
	}
	if c := (Confidence{}).Score(it, ctx); c.Value != .75 {
		t.Fatalf("confidence=%+v", c)
	}
	it.Features.LabelOverride = "UnKnOwN"
	if u := (Unknown{}).Score(it, ctx); u.Value != 1 || !isUnknown(it, ctx) {
		t.Fatalf("unknown override=%+v", u)
	}
	it.Features.LabelOverride = "boat"
	it.Features.OverrideBy = "watch officer"
	if u := (Unknown{}).Score(it, ctx); u.Value != 0 || !strings.Contains(u.Note, "watch officer") || isUnknown(it, ctx) {
		t.Fatalf("resolved=%+v", u)
	}
	it.Features = nil
	if u := (Unknown{}).Score(it, ctx); u != (Result{}) || isUnknown(it, ctx) {
		t.Fatalf("empty=%+v", u)
	}
	if c := (Confidence{}).Score(it, ctx); c != (Result{}) {
		t.Fatalf("empty confidence=%+v", c)
	}
	if label, conf := TopLabel(nil, .5); label != "" || conf != 0 {
		t.Fatal("nil item has label")
	}
}

func TestIntentEmbeddingsRespectUnknownsAndHumanLabels(t *testing.T) {
	it := &pb.Item{Features: &pb.Features{Embedding: []float32{1, 0}, Detections: []*pb.Detection{{Label: "boat", Confidence: .9}}}}
	ctx := &Context{Policy: &pb.Policy{Interest: &pb.Interest{PositiveEmbedding: []float32{1, 0}, BoostFactor: 3}}}
	if r := (Interest{}).Score(it, ctx); r.Value != 1 || r.Multiplier != 3 || r.Suppressed {
		t.Fatalf("positive=%+v", r)
	}
	ctx.Policy.Interest = &pb.Interest{PositiveEmbedding: []float32{0, 1}, NegativeEmbedding: []float32{1, 0}}
	if r := (Interest{}).Score(it, ctx); !r.Suppressed || r.Multiplier != DefaultSuppressFactor {
		t.Fatalf("negative=%+v", r)
	}
	it.Features.Unknownness = 1
	if r := (Interest{}).Score(it, ctx); r.Suppressed || r.Value != 0 {
		t.Fatalf("unknown suppressed=%+v", r)
	}
	it.Features.LabelOverride = "boat"
	if r := (Interest{}).Score(it, ctx); !r.Suppressed {
		t.Fatalf("resolved unknown=%+v", r)
	}
	ctx.Policy.Interest = &pb.Interest{PositiveEmbedding: []float32{0, 1}}
	if r := (Interest{}).Score(it, ctx); r != (Result{}) {
		t.Fatalf("orthogonal=%+v", r)
	}
	ctx.Policy.Interest = &pb.Interest{BoostLabels: []string{"boat"}, BoostWeights: map[string]float64{"boat": 4}}
	if r := (Interest{}).Score(it, ctx); r.Multiplier != 4 || r.Value != 1 {
		t.Fatalf("weighted boost=%+v", r)
	}
	it.Features = nil
	if r := (Interest{}).Score(it, ctx); r != (Result{}) {
		t.Fatalf("empty intent=%+v", r)
	}
}
