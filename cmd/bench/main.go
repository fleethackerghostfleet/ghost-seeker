// Command bench reports edge-triage and command-model metrics against the mission oracle.
package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/bench"
)

func main() {
	n := flag.Int("n", 6000, "frames per seed")
	seeds := flag.String("seeds", "101,102,103", "comma-separated seeds (held-out test set; tune on other seeds)")
	flag.Parse()
	models := []string{"maritime", "air", "sar"}
	sum := map[string]*bench.Result{}
	var count int
	for _, s := range strings.Split(*seeds, ",") {
		seed, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			panic(err)
		}
		frames := bench.Generate(seed, *n)
		count++
		for _, m := range models {
			r := bench.Evaluate(m, frames)
			if sum[m] == nil {
				sum[m] = &bench.Result{Model: m}
			}
			add(sum[m], r)
		}
	}
	fmt.Printf("Seeds %s · %d frames each\n\n", *seeds, *n)
	fmt.Println("| Model | Edge drop | Edge miss P1 | Edge miss P1–2 | Tier exact | ±1 tier | P1 recall | P1 precision | Critical miss | False flash | Flash load | NDCG@20 | Review burden |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, m := range models {
		r := sum[m]
		c := float64(count)
		fmt.Printf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %.3f | %s |\n", m,
			pct(r.EdgeDrop/c), pct(r.EdgeMissP1/c), pct(r.EdgeMissP12/c), pct(r.TierExact/c), pct(r.TierWithin1/c),
			pct(r.P1Recall/c), pct(r.P1Precision/c), pct(r.CriticalMiss/c), pct(r.FalseFlash/c), pct(r.FlashLoad/c), r.NDCG/c, pct(r.Burden/c))
	}
}

func add(t *bench.Result, r bench.Result) {
	t.EdgeDrop += r.EdgeDrop
	t.EdgeMissP1 += r.EdgeMissP1
	t.EdgeMissP12 += r.EdgeMissP12
	t.TierExact += r.TierExact
	t.TierWithin1 += r.TierWithin1
	t.P1Recall += r.P1Recall
	t.P1Precision += r.P1Precision
	t.CriticalMiss += r.CriticalMiss
	t.FalseFlash += r.FalseFlash
	t.FlashLoad += r.FlashLoad
	t.NDCG += r.NDCG
	t.Burden += r.Burden
}

func pct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }
