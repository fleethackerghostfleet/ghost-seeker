// Command bench reports edge-triage and command-model metrics against the mission oracle.
package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/bench"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func main() {
	n := flag.Int("n", 6000, "frames per seed")
	seeds := flag.String("seeds", "101,102,103", "comma-separated seeds (held-out test set; tune on other seeds)")
	unknowns := flag.Bool("unknowns", true, "inject unknown contacts using a separate seeded RNG")
	beforeUnknown := flag.Bool("before-unknown", false, "zero unknown command and edge weights to reproduce closed-set behavior")
	flag.Parse()
	if *beforeUnknown {
		for _, m := range catalog.Models {
			m.W["unknown"] = 0
		}
		catalog.EdgeW["unknown"] = 0
	}
	models := []string{"maritime", "air", "sar"}
	sum := map[string]*bench.Result{}
	var count int
	for _, s := range strings.Split(*seeds, ",") {
		seed, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			panic(err)
		}
		frames := bench.GenerateWithUnknowns(seed, *n, *unknowns)
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
	fmt.Println("\nUnknown contacts (contact scores, not the frame's top contact; persistent includes approaching):")
	fmt.Println("| Model | Persistent n | Edge kept | Edge kept AND ≤P2 | Approaching n | Scored P1 | Transient n | Scored P4 |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, m := range models {
		r := sum[m]
		fmt.Printf("| %s | %d | %s | %s | %d | %s | %d | %s |\n", m, r.UnknownPersistent, share(r.UnknownKept, r.UnknownPersistent), share(r.UnknownP12, r.UnknownPersistent), r.UnknownApproaching, share(r.UnknownP1, r.UnknownApproaching), r.UnknownTransient, share(r.UnknownP4, r.UnknownTransient))
	}
}

func add(t *bench.Result, r bench.Result) {
	t.UnknownPersistent += r.UnknownPersistent
	t.UnknownKept += r.UnknownKept
	t.UnknownP12 += r.UnknownP12
	t.UnknownApproaching += r.UnknownApproaching
	t.UnknownP1 += r.UnknownP1
	t.UnknownTransient += r.UnknownTransient
	t.UnknownP4 += r.UnknownP4
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

func share(n, total int) string {
	if total == 0 {
		return "—"
	}
	return pct(float64(n) / float64(total))
}
