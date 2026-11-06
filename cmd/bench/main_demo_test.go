//go:build !prod

package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/bench"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestBenchReportAndClosedSetOption(t *testing.T) {
	for _, closedSet := range []bool{false, true} {
		t.Run(strconv.FormatBool(closedSet), func(t *testing.T) {
			weights := map[string]float64{}
			for name, model := range catalog.Models {
				weights[name] = model.W["unknown"]
			}
			edgeWeight := catalog.EdgeW["unknown"]
			t.Cleanup(func() {
				for name, weight := range weights {
					catalog.Models[name].W["unknown"] = weight
				}
				catalog.EdgeW["unknown"] = edgeWeight
			})
			output := runBench(t, "-n=12", "-seeds=17, 18", "-unknowns=false", "-before-unknown="+strconv.FormatBool(closedSet))
			if !strings.Contains(output, "Seeds 17, 18 · 12 frames each") || !strings.Contains(output, "NDCG@20") {
				t.Fatalf("missing report headings: %s", output)
			}
			for _, model := range []string{"maritime", "air", "sar"} {
				var meanDrop float64
				for _, seed := range []int64{17, 18} {
					frames := bench.GenerateWithUnknowns(seed, 12, false)
					if len(frames) != 12 {
						t.Fatalf("seed %d generated %d frames, want 12", seed, len(frames))
					}
					detections := 0
					for _, frame := range frames {
						detections += len(frame.Dets)
					}
					result := bench.Evaluate(model, frames)
					if detections == 0 || result.Frames != 12 || result.Kept == 0 {
						t.Fatalf("seed %d model %s did not exercise scoring: detections=%d frames=%d kept=%d", seed, model, detections, result.Frames, result.Kept)
					}
					meanDrop += result.EdgeDrop / 2
				}
				if !strings.Contains(output, fmt.Sprintf("| %s | %s |", model, pct(meanDrop))) {
					t.Fatalf("report missing measured edge-drop result for %s: %s", model, output)
				}
				if strings.Count(output, "| "+model+" |") != 2 {
					t.Fatalf("expected main and unknown rows for %s: %s", model, output)
				}
				if closedSet && catalog.Models[model].W["unknown"] != 0 {
					t.Fatalf("closed-set model %s retains unknown weight", model)
				}
			}
			if strings.Contains(output, "NaN") || strings.Contains(output, "Inf") {
				t.Fatalf("non-finite report: %s", output)
			}
			if closedSet && catalog.EdgeW["unknown"] != 0 {
				t.Fatal("closed-set edge retains unknown weight")
			}
		})
	}
}
