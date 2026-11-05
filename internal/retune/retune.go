// Package retune implements offline, serial weight proposals. It temporarily
// swaps catalog models to exercise the production scorer, restoring them on return.
package retune

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/bench"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

var tuning sync.Mutex

type Example struct {
	ID     string
	Dets   []command.Detection
	Human  int
	Credit map[string]bool
	Night  bool
}
type Metrics struct {
	Count                    int
	Exact, Within1, P1Recall float64
	CriticalMiss             int
}
type Change struct {
	Class         string
	Before, After float64
	Count         int
}
type Gate struct {
	Seed   int64
	Result bench.Result
	Error  string
}
type ModelResult struct {
	Key            string
	Train, Holdout int
	Before, After  Metrics
	Changes        []Change
	Gate           []Gate
	Accepted       bool
	TooLittle      bool
}
type Proposal struct {
	W        map[string]float64 `json:"w"`
	Lift     map[string]float64 `json:"lift"`
	Accepted bool               `json:"accepted"`
}
type Result struct {
	Raw, Effective, Usable, Excluded, Conflicts int
	Reviewers                                   map[string]int
	Models                                      []ModelResult
	Weights                                     map[string]Proposal
}

func ModelKey(r reviews.Record) string {
	if r.ModelKey != "" {
		if _, ok := catalog.Models[r.ModelKey]; ok {
			return r.ModelKey
		}
		return ""
	}
	if _, ok := catalog.Models[r.Model]; ok {
		return r.Model
	}
	for k, m := range catalog.Models {
		if m.Name == r.Model {
			return k
		}
	}
	return ""
}
func Holdout(id string) bool { h := fnv.New64a(); h.Write([]byte(id)); return h.Sum64()%5 == 0 }
func predict(key string, e Example) int {
	ss := make([]float64, 0, len(e.Dets))
	for _, d := range e.Dets {
		ss = append(ss, command.ScoreDet(d, nil, key, e.Night).Score)
	}
	return command.TierFor(command.FrameScore(ss))
}
func Measure(key string, es []Example) Metrics {
	m := Metrics{Count: len(es)}
	exact, within, p1, hit := 0, 0, 0, 0
	for _, e := range es {
		t := predict(key, e)
		if t == e.Human {
			exact++
		}
		if abs(t-e.Human) <= 1 {
			within++
		}
		if e.Human == 1 {
			p1++
			if t == 1 {
				hit++
			}
		}
		if e.Human <= 2 && t >= 3 {
			m.CriticalMiss++
		}
	}
	if len(es) > 0 {
		m.Exact = float64(exact) / float64(len(es))
		m.Within1 = float64(within) / float64(len(es))
	}
	if p1 > 0 {
		m.P1Recall = float64(hit) / float64(p1)
	}
	return m
}
func objective(key string, es []Example) float64 {
	v := 0.0
	for _, e := range es {
		t := predict(key, e)
		if t != e.Human {
			v++
		}
		if e.Human <= 2 && t > e.Human {
			v += 2 * float64(t-e.Human)
		}
	}
	return v
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func clone(m map[string]float64) map[string]float64 {
	v := map[string]float64{}
	for k, w := range m {
		v[k] = w
	}
	return v
}

func prepare(rs []reviews.Record, r *Result) (map[string][]Example, error) {
	groups := map[string][]reviews.Record{}
	r.Reviewers = map[string]int{}
	for _, v := range rs {
		r.Reviewers[v.Reviewer]++
		if v.Reason == "wrong_detection" || v.HumanTier == 0 {
			r.Excluded++
			continue
		}
		key := ModelKey(v)
		if key == "" {
			return nil, fmt.Errorf("record %s: unknown mission model %q (key %q)", v.ID, v.Model, v.ModelKey)
		}
		if v.ItemID == "" || v.HumanTier < 1 || v.HumanTier > 4 || len(v.Detections) == 0 {
			return nil, fmt.Errorf("record %s: invalid item, tier or detections", v.ID)
		}
		r.Usable++
		groups[key+"\x00"+v.ItemID] = append(groups[key+"\x00"+v.ItemID], v)
	}
	out := map[string][]Example{}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		sort.Slice(g, func(i, j int) bool {
			if g[i].ReviewedAt.Equal(g[j].ReviewedAt) {
				return g[i].ID < g[j].ID
			}
			return g[i].ReviewedAt.Before(g[j].ReviewedAt)
		})
		latest := map[string]reviews.Record{}
		for _, v := range g {
			latest[v.Reviewer] = v
		}
		tiers := []int{}
		credit := map[string]bool{}
		for _, v := range latest {
			tiers = append(tiers, v.HumanTier)
			if v.Reason != "" {
				found := false
				for _, d := range v.Detections {
					if d.Class == v.Reason {
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("record %s: reason does not name a detection", v.ID)
				}
				credit[v.Reason] = true
			}
		}
		sort.Ints(tiers)
		if tiers[0] != tiers[len(tiers)-1] {
			r.Conflicts++
		}
		v := g[len(g)-1]
		key := ModelKey(v)
		night := command.OpticalNight(&v.Before)
		if len(credit) == 0 {
			best := -1.0
			for _, d := range v.Detections {
				s := command.ScoreDet(d, nil, key, night).Score
				if s > best {
					best = s
					credit = map[string]bool{d.Class: true}
				}
			}
		}
		out[key] = append(out[key], Example{ID: v.ItemID, Dets: v.Detections, Human: tiers[len(tiers)/2], Credit: credit, Night: night})
	}
	return out, nil
}

func Run(paths []string, minRecords int) (Result, error) {
	tuning.Lock()
	defer tuning.Unlock()
	r := Result{Weights: map[string]Proposal{}}
	if minRecords < 1 {
		return r, fmt.Errorf("min-records must be positive")
	}
	rs, n, err := reviews.ReadEffective(paths)
	if err != nil {
		return r, err
	}
	r.Raw = n
	r.Effective = len(rs)
	data, err := prepare(rs, &r)
	if err != nil {
		return r, err
	}
	keys := []string{}
	for key := range catalog.Models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	frames := map[int64][]bench.Frame{}
	for seed := int64(101); seed <= 103; seed++ {
		frames[seed] = bench.Generate(seed, 4000)
	}
	for _, key := range keys {
		func() {
			base := catalog.Models[key]
			defer func() { catalog.Models[key] = base }()
			candidate := base
			candidate.W = clone(base.W)
			catalog.Models[key] = candidate
			m := ModelResult{Key: key}
			train, hold := []Example{}, []Example{}
			counts := map[string]int{}
			for _, e := range data[key] {
				if Holdout(e.ID) {
					hold = append(hold, e)
				} else {
					train = append(train, e)
					for c := range e.Credit {
						counts[c]++
					}
				}
			}
			m.Train = len(train)
			m.Holdout = len(hold)
			m.Before = Measure(key, hold)
			eligible := []string{}
			for c, n := range counts {
				if _, ok := base.W[c]; ok && n >= minRecords {
					eligible = append(eligible, c)
				}
			}
			sort.Strings(eligible)
			m.TooLittle = len(eligible) == 0 || len(hold) == 0
			if !m.TooLittle {
				for pass := 0; pass < 3; pass++ {
					changed := false
					for _, c := range eligible {
						old := candidate.W[c]
						best := old
						loss := objective(key, train)
						for step := -6; step <= 6; step++ {
							w := math.Round(min(1, max(0, base.W[c]+float64(step)*.05))*1e9) / 1e9
							candidate.W[c] = w
							v := objective(key, train)
							if v < loss-1e-9 || (math.Abs(v-loss) < 1e-9 && math.Abs(w-base.W[c]) < math.Abs(best-base.W[c])-1e-9) {
								loss = v
								best = w
							}
						}
						candidate.W[c] = best
						if best != old {
							changed = true
						}
					}
					if !changed {
						break
					}
				}
			}
			m.Accepted = true
			for seed := int64(101); seed <= 103; seed++ {
				v := bench.Evaluate(key, frames[seed])
				g := Gate{Seed: seed, Result: v}
				if err := bench.CheckFloor(v); err != nil {
					g.Error = err.Error()
					m.Accepted = false
				}
				m.Gate = append(m.Gate, g)
			}
			for _, c := range eligible {
				m.Changes = append(m.Changes, Change{Class: c, Before: base.W[c], After: candidate.W[c], Count: counts[c]})
			}
			m.After = Measure(key, hold)
			weights := candidate.W
			if !m.Accepted {
				weights = base.W
			}
			r.Weights[key] = Proposal{W: clone(weights), Lift: clone(base.Lift), Accepted: m.Accepted && !m.TooLittle}
			r.Models = append(r.Models, m)
		}()
	}
	return r, nil
}

func (r Result) Markdown() string {
	var b strings.Builder
	fmt.Fprint(&b, "# Human review retuning proposal\n\n")
	fmt.Fprintf(&b, "Records: %d unique log events; %d effective reviews; %d usable labels; %d excluded (dismissed/wrong detection). Conflicting items: %d (per mission).\n\n", r.Raw, r.Effective, r.Usable, r.Excluded, r.Conflicts)
	fmt.Fprintln(&b, "Per-reviewer effective records:")
	names := []string{}
	for n := range r.Reviewers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "- %s: %d\n", strings.ReplaceAll(n, "\n", " "), r.Reviewers[n])
	}
	fmt.Fprint(&b, "\nNo directives. Latest label per reviewer/item/mission, upper median for even label counts. Item-id FNV-1a hash: 80/20 train/holdout. Named reason classes receive training credit; otherwise the highest-scoring class does. Only train labels influence bounded coordinate search. Lifts unchanged.\n\n")
	for _, m := range r.Models {
		fmt.Fprintf(&b, "## %s\n\nTrain items: %d; holdout items: %d.\n\n", m.Key, m.Train, m.Holdout)
		if m.TooLittle {
			fmt.Fprint(&b, "**Too little data**: need an eligible class and nonempty holdout. No fitting performed.\n\n")
		}
		fmt.Fprintln(&b, "| Holdout metric | Before | Candidate |\n|---|---:|---:|")
		fmt.Fprintf(&b, "| Exact tier | %.1f%% | %.1f%% |\n| ±1 tier | %.1f%% | %.1f%% |\n| Human-P1 recall | %.1f%% | %.1f%% |\n| Human P1/P2 scored P3/P4 | %d | %d |\n\n", m.Before.Exact*100, m.After.Exact*100, m.Before.Within1*100, m.After.Within1*100, m.Before.P1Recall*100, m.After.P1Recall*100, m.Before.CriticalMiss, m.After.CriticalMiss)
		fmt.Fprintln(&b, "P1 recall is 0 when there are no human-P1 holdout items.\n\n| Class | Train items credited | Before | Candidate |\n|---|---:|---:|---:|")
		for _, c := range m.Changes {
			fmt.Fprintf(&b, "| %s | %d | %.2f | %.2f |\n", c.Class, c.Count, c.Before, c.After)
		}
		fmt.Fprintln(&b, "\nBenchmark gate (4,000 frames/seed; floors: NDCG ≥ .95, P1 recall ≥ .85, critical miss ≤ .01, edge miss P1 ≤ .01):\n\n| Seed | NDCG | P1 recall | Critical miss | Edge miss P1 | Result |\n|---|---:|---:|---:|---:|---|")
		for _, g := range m.Gate {
			status := "PASS"
			if g.Error != "" {
				status = "FAIL: " + g.Error
			}
			fmt.Fprintf(&b, "| %d | %.3f | %.3f | %.3f | %.3f | %s |\n", g.Seed, g.Result.NDCG, g.Result.P1Recall, g.Result.CriticalMiss, g.Result.EdgeMissP1, status)
		}
		if !m.Accepted {
			fmt.Fprintln(&b, "\n**REJECTED**: candidate fails benchmark floors; JSON retains original weights.")
		} else if !m.TooLittle {
			fmt.Fprintln(&b, "\n**ACCEPTED for manual review only.**")
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}
