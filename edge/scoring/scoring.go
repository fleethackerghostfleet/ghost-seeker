// Package scoring turns an item plus the current policy into a priority with
// reasons. Scorers are pluggable; the image model, operator interest and
// "unknownness" are each one scorer. Combination:
//
//	priority = Σ wᵢ·valueᵢ × Π multiplierᵢ × freshness(age, type) + pull boost
//
// A suppressed item (matched a negative interest) keeps a small positive
// priority: its manifest entry and FACT still go, images sort last. Nothing
// is ever scored to zero and nothing is dropped.
package scoring

import (
	"math"
	"strings"
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/vec"
)

// Context is everything a scorer may consult besides the item.
type Context struct {
	Policy *pb.Policy
	Now    time.Time
}

type Result struct {
	Value      float64 // additive contribution, nominally 0..1
	Multiplier float64 // multiplicative; 0 means "unset" and is treated as 1
	Suppressed bool
	Note       string
}

type Scorer interface {
	Name() string
	Score(it *pb.Item, ctx *Context) Result
}

// Engine combines scorers with weights.
type Engine struct {
	Scorers []Scorer
	Weights map[string]float64 // default weight per scorer name
}

const (
	DefaultBoostFactor    = 2.0
	DefaultSuppressFactor = 0.05
	DefaultUnknownMin     = 0.5
	DefaultDetectionMin   = 0.25
)

// Default returns the standard engine: base value by type, unknownness,
// operator interest, and detection confidence.
func Default() *Engine {
	return &Engine{
		Scorers: []Scorer{BaseValue{}, Unknown{}, Interest{}, Confidence{}},
		Weights: map[string]float64{"base": 1, "unknown": 3, "interest": 1, "confidence": 0.5},
	}
}

func (e *Engine) weight(name string, p *pb.Policy) float64 {
	if p != nil && p.ScorerWeights != nil {
		if w, ok := p.ScorerWeights[name]; ok {
			return w
		}
	}
	if w, ok := e.Weights[name]; ok {
		return w
	}
	return 1
}

// Score computes the priority of one item. pullBoost is the shore-requested
// boost (0 if none).
func (e *Engine) Score(it *pb.Item, ctx *Context, pullBoost float64) *pb.Score {
	out := &pb.Score{ComputedAtUnixMs: ctx.Now.UnixMilli()}
	sum, mult := 0.0, 1.0
	for _, s := range e.Scorers {
		r := s.Score(it, ctx)
		w := e.weight(s.Name(), ctx.Policy)
		if r.Value != 0 {
			sum += w * r.Value
		}
		if r.Multiplier > 0 && r.Multiplier != 1 {
			mult *= r.Multiplier
		}
		if r.Suppressed {
			out.Suppressed = true
		}
		if r.Note != "" || r.Value != 0 {
			out.Reasons = append(out.Reasons, &pb.Reason{Scorer: s.Name(), Value: w * r.Value, Note: r.Note})
		}
	}
	f := Freshness(it, ctx.Now)
	out.Reasons = append(out.Reasons, &pb.Reason{Scorer: "freshness", Value: f})
	p := sum * mult * f
	if p < 0.001 {
		p = 0.001 // never zero: everything eventually goes if the link allows
	}
	if pullBoost > 0 {
		p += pullBoost
		out.Reasons = append(out.Reasons, &pb.Reason{Scorer: "pull", Value: pullBoost, Note: "requested by shore"})
	}
	out.Priority = p
	return out
}

// Freshness decays information value with age but never below 0.25, so old
// items still drain when the link is idle. Half-lives by type.
func Freshness(it *pb.Item, now time.Time) float64 {
	age := now.Sub(time.UnixMilli(it.CapturedAtUnixMs))
	if age < 0 {
		age = 0
	}
	var half time.Duration
	switch it.Type {
	case pb.ItemType_ITEM_TYPE_RECORD, pb.ItemType_ITEM_TYPE_DIGEST:
		half = 6 * time.Hour
	case pb.ItemType_ITEM_TYPE_TELEMETRY:
		half = 30 * time.Minute
	default:
		half = 2 * time.Hour
	}
	return 0.25 + 0.75*math.Exp2(-age.Hours()/half.Hours())
}

// TopLabel returns the item's effective label and confidence: the human
// override if set, else the most confident detection above the threshold.
func TopLabel(it *pb.Item, minConf float32) (string, float32) {
	f := it.GetFeatures()
	if f == nil {
		return "", 0
	}
	if f.LabelOverride != "" {
		return f.LabelOverride, 1
	}
	var best *pb.Detection
	for _, d := range f.Detections {
		if d.Confidence < minConf {
			continue
		}
		if best == nil || d.Confidence > best.Confidence {
			best = d
		}
	}
	if best == nil {
		return "", 0
	}
	return best.Label, best.Confidence
}

func thresholds(p *pb.Policy) (unknownMin, detMin float32) {
	unknownMin, detMin = DefaultUnknownMin, DefaultDetectionMin
	if t := p.GetThresholds(); t != nil {
		if t.UnknownMin > 0 {
			unknownMin = t.UnknownMin
		}
		if t.DetectionMinConf > 0 {
			detMin = t.DetectionMinConf
		}
	}
	return
}

// BaseValue: small structured records and digests are always worth sending;
// imagery starts lower and earns priority from the other scorers.
type BaseValue struct{}

func (BaseValue) Name() string { return "base" }
func (BaseValue) Score(it *pb.Item, _ *Context) Result {
	switch it.Type {
	case pb.ItemType_ITEM_TYPE_RECORD:
		return Result{Value: 1.0, Note: "record"}
	case pb.ItemType_ITEM_TYPE_DIGEST:
		return Result{Value: 1.0, Note: "digest"}
	case pb.ItemType_ITEM_TYPE_TELEMETRY:
		return Result{Value: 0.3}
	default:
		return Result{Value: 0.2}
	}
}

// Unknown: unidentified things are the most interesting. A human label
// override resolves the unknown (unless the override itself is "unknown").
type Unknown struct{}

func (Unknown) Name() string { return "unknown" }
func (Unknown) Score(it *pb.Item, ctx *Context) Result {
	f := it.GetFeatures()
	if f == nil {
		return Result{}
	}
	u := float64(f.Unknownness)
	if f.LabelOverride != "" {
		if strings.EqualFold(f.LabelOverride, "unknown") {
			u = 1
		} else {
			return Result{Note: "resolved by " + f.OverrideBy}
		}
	}
	unknownMin, _ := thresholds(ctx.Policy)
	if u < float64(unknownMin) {
		return Result{Value: u * 0.2}
	}
	return Result{Value: u, Note: "unknown"}
}

// Interest applies the operator's current scope: label boosts/suppressions
// and embedding similarity to the positive/negative intent vectors.
type Interest struct{}

func (Interest) Name() string { return "interest" }
func (Interest) Score(it *pb.Item, ctx *Context) Result {
	in := ctx.Policy.GetInterest()
	if in == nil {
		return Result{}
	}
	_, detMin := thresholds(ctx.Policy)
	label, _ := TopLabel(it, detMin)
	boost := in.BoostFactor
	if boost <= 0 {
		boost = DefaultBoostFactor
	}
	sup := in.SuppressFactor
	if sup <= 0 {
		sup = DefaultSuppressFactor
	}
	labels := allLabels(it, detMin)
	for _, l := range in.SuppressLabels {
		if hasLabel(labels, l) && !isUnknown(it, ctx) {
			return Result{Multiplier: sup, Suppressed: true, Note: "suppressed: " + l}
		}
	}
	for _, l := range in.BoostLabels {
		if hasLabel(labels, l) {
			return Result{Value: 1, Multiplier: boost, Note: "boosted: " + l}
		}
	}
	emb := it.GetFeatures().GetEmbedding()
	if len(emb) == 0 || (len(in.PositiveEmbedding) == 0 && len(in.NegativeEmbedding) == 0) {
		return Result{}
	}
	pos := vec.Cosine(emb, in.PositiveEmbedding)
	neg := vec.Cosine(emb, in.NegativeEmbedding)
	if len(in.NegativeEmbedding) > 0 && neg > 0.5 && neg > pos && !isUnknown(it, ctx) {
		return Result{Multiplier: sup, Suppressed: true, Note: "suppressed: similar to ignore intent"}
	}
	if len(in.PositiveEmbedding) > 0 && pos > 0.3 {
		return Result{Value: pos, Multiplier: 1 + (boost-1)*pos, Note: "matches intent"}
	}
	_ = label
	return Result{}
}

func isUnknown(it *pb.Item, ctx *Context) bool {
	unknownMin, _ := thresholds(ctx.Policy)
	f := it.GetFeatures()
	if f == nil {
		return false
	}
	if f.LabelOverride != "" {
		return strings.EqualFold(f.LabelOverride, "unknown")
	}
	return f.Unknownness >= unknownMin
}

// allLabels is what a policy's boost/suppress labels are matched against:
// the operator's override (authoritative, so it stands alone), the detector
// labels above the policy threshold, and the engine's zero-shot subtype
// (tags["subtype"], set by the sighting pipeline), so a directive like
// "ignore cargo ships" reaches items the detector only calls "boat".
func allLabels(it *pb.Item, minConf float32) []string {
	f := it.GetFeatures()
	if f == nil {
		return nil
	}
	if f.LabelOverride != "" {
		return []string{f.LabelOverride}
	}
	var out []string
	for _, d := range f.Detections {
		if d.Confidence >= minConf {
			out = append(out, d.Label)
		}
	}
	if sub := f.GetTags()["subtype"]; sub != "" {
		out = append(out, sub)
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	w := norm(want)
	for _, l := range labels {
		n := norm(l)
		if n == w || strings.Contains(n, w) || strings.Contains(w, n) {
			return true
		}
	}
	return false
}

func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "s")
	return strings.ReplaceAll(s, "_", " ")
}

// Confidence: a confident detection of anything is worth a little more than
// empty ocean, so "something is there" beats "nothing is there".
type Confidence struct{}

func (Confidence) Name() string { return "confidence" }
func (Confidence) Score(it *pb.Item, ctx *Context) Result {
	_, detMin := thresholds(ctx.Policy)
	label, conf := TopLabel(it, detMin)
	if label == "" {
		return Result{}
	}
	return Result{Value: float64(conf), Note: label}
}
