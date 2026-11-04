package bench

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
)

// Frame is one synthetic capture: library-image contacts with drawn confidence/attributes plus injected false positives.
type Frame struct {
	Src  string
	Dets []command.Detection
	FP   []bool
	Edge float64
}

const (
	FPRate     = .3 // chance a frame carries one spurious detection
	WindowSize = 200
	TopK       = 20
)

// Generate draws n frames deterministically from seed using the generated imagery library.
func Generate(seed int64, n int) []Frame {
	rng := rand.New(rand.NewSource(seed))
	frames := make([]Frame, 0, n)
	for len(frames) < n {
		src := catalog.Sources[rng.Intn(len(catalog.Sources))]
		lib := catalog.Library[src.ID]
		if len(lib) == 0 {
			continue
		}
		img := lib[rng.Intn(len(lib))]
		f := Frame{Src: src.ID}
		for _, d := range img.Dets {
			f.Dets = append(f.Dets, command.Detection{Class: d.Cls, Conf: round2(between(rng, .55, .98)), Attrs: sensor.Attributes(rng, d.Cls)})
			f.FP = append(f.FP, false)
		}
		if rng.Float64() < FPRate {
			cls := weighted(rng, catalog.Pools[src.Scene])
			f.Dets = append(f.Dets, command.Detection{Class: cls, Conf: round2(between(rng, .35, .7)), Attrs: sensor.Attributes(rng, cls)})
			f.FP = append(f.FP, true)
		}
		f.Edge = sensor.EdgeScore(rng, f.Dets)
		frames = append(frames, f)
	}
	return frames
}

// Result holds the benchmark metrics for one model on one frame set.
type Result struct {
	Model                   string
	Frames, Kept            int
	EdgeDrop                float64 // share of frames dropped at the edge
	EdgeMissP1, EdgeMissP12 float64 // share of oracle P1 (P1–P2) frames dropped at the edge
	TierExact, TierWithin1  float64 // on kept frames
	P1Recall, P1Precision   float64
	CriticalMiss            float64 // oracle P1 scored P3/P4
	FalseFlash              float64 // oracle P3/P4 scored P1
	FlashLoad               float64 // share of kept frames scored P1
	NDCG                    float64 // mean NDCG@TopK per window
	Burden                  float64 // mean share of a window reviewed (in score order) to reach every oracle P1
}

// Evaluate scores frames with model against the model's mission oracle.
func Evaluate(model string, frames []Frame) Result {
	mission := Missions[model]
	r := Result{Model: model, Frames: len(frames)}
	type scored struct {
		central  float64
		tier, gt int
	}
	var kept []scored
	var p1, p12, p1Drop, p12Drop int
	now := time.Unix(0, 0)
	for _, f := range frames {
		gt := FrameTier(mission, f.Dets, f.FP)
		if gt == 1 {
			p1++
		}
		if gt <= 2 {
			p12++
		}
		if f.Edge < sensor.EdgeKeep {
			if gt == 1 {
				p1Drop++
			}
			if gt <= 2 {
				p12Drop++
			}
			continue
		}
		item := command.Item{SourceID: f.Src, TS: now, Dets: f.Dets}
		command.RescoreAt(&item, model, nil, now)
		kept = append(kept, scored{item.Central, item.CentralTier, gt})
	}
	r.Kept = len(kept)
	r.EdgeDrop = 1 - ratio(len(kept), len(frames))
	r.EdgeMissP1, r.EdgeMissP12 = ratio(p1Drop, p1), ratio(p12Drop, p12)
	var exact, within, gt1, pred1, hit1, crit, low, falseFlash int
	for _, s := range kept {
		if s.tier == s.gt {
			exact++
		}
		if abs(s.tier-s.gt) <= 1 {
			within++
		}
		if s.gt == 1 {
			gt1++
			if s.tier == 1 {
				hit1++
			}
			if s.tier >= 3 {
				crit++
			}
		}
		if s.tier == 1 {
			pred1++
		}
		if s.gt >= 3 {
			low++
			if s.tier == 1 {
				falseFlash++
			}
		}
	}
	n := len(kept)
	r.TierExact, r.TierWithin1 = ratio(exact, n), ratio(within, n)
	r.P1Recall, r.P1Precision = ratio(hit1, gt1), ratio(hit1, pred1)
	r.CriticalMiss, r.FalseFlash, r.FlashLoad = ratio(crit, gt1), ratio(falseFlash, low), ratio(pred1, n)
	var ndcgSum, burdenSum float64
	var windows, burdenWindows int
	for start := 0; start+WindowSize <= n; start += WindowSize {
		w := append([]scored(nil), kept[start:start+WindowSize]...)
		sort.SliceStable(w, func(i, j int) bool { return w[i].central > w[j].central })
		gains := make([]int, len(w))
		for i, s := range w {
			gains[i] = s.gt
		}
		ndcgSum += NDCG(gains, TopK)
		windows++
		if b, ok := Burden(gains); ok {
			burdenSum += b
			burdenWindows++
		}
	}
	r.NDCG, r.Burden = ratioF(ndcgSum, windows), ratioF(burdenSum, burdenWindows)
	return r
}

// Gain is the graded relevance of an oracle tier.
func Gain(tier int) float64 { return math.Pow(2, float64(4-tier)) - 1 }

// NDCG@k of oracle tiers listed in the model's ranked order.
func NDCG(tiers []int, k int) float64 {
	dcg := func(ts []int) float64 {
		s := 0.0
		for i := 0; i < len(ts) && i < k; i++ {
			s += Gain(ts[i]) / math.Log2(float64(i+2))
		}
		return s
	}
	ideal := append([]int(nil), tiers...)
	sort.Ints(ideal)
	if d := dcg(ideal); d > 0 {
		return dcg(tiers) / d
	}
	return 1
}

// Burden is the share of a ranked window an operator reviews before seeing every oracle P1; ok is false without P1s.
func Burden(tiers []int) (float64, bool) {
	last := -1
	for i, t := range tiers {
		if t == 1 {
			last = i
		}
	}
	if last < 0 {
		return 0, false
	}
	return float64(last+1) / float64(len(tiers)), true
}

func weighted(rng *rand.Rand, w map[string]float64) string {
	keys := make([]string, 0, len(w))
	total := 0.0
	for k, v := range w {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	x := rng.Float64() * total
	for _, k := range keys {
		if x -= w[k]; x <= 0 {
			return k
		}
	}
	return keys[len(keys)-1]
}

func between(rng *rand.Rand, lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }
func round2(v float64) float64                       { return math.Round(v*100) / 100 }
func ratio(a, b int) float64                         { return ratioF(float64(a), b) }
func ratioF(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
