package retune

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func (r Result) writeLiveOutcomes(b *strings.Builder) {
	fmt.Fprint(b, "## Live-review outcomes\n\n")
	latest := map[string]reviews.Record{}
	for _, v := range r.LiveRecords {
		key := v.ModelKey + "\x00" + v.ItemID + "\x00" + v.Reviewer
		old, ok := latest[key]
		if !ok || v.ReviewedAt.After(old.ReviewedAt) || (v.ReviewedAt.Equal(old.ReviewedAt) && v.ID > old.ID) {
			latest[key] = v
		}
	}
	agreed, wrong, pairs, same := 0, 0, 0, 0
	var matrix [4][5]int
	classes := map[string]reviews.Counts{}
	items := map[string][]int{}
	for _, v := range latest {
		match := v.ModelTier == v.HumanTier
		if match {
			agreed++
		}
		if v.Reason == "wrong_detection" {
			wrong++
		}
		if v.ModelTier >= 1 && v.ModelTier <= 4 && v.HumanTier >= 0 && v.HumanTier <= 4 {
			matrix[v.ModelTier-1][v.HumanTier]++
		}
		seen := map[string]bool{}
		for _, d := range v.Detections {
			if seen[d.Class] {
				continue
			}
			seen[d.Class] = true
			c := classes[d.Class]
			c.Count++
			if match {
				c.Agreed++
			}
			classes[d.Class] = c
		}
		key := v.ModelKey + "\x00" + v.ItemID
		for _, tier := range items[key] {
			pairs++
			if tier == v.HumanTier {
				same++
			}
		}
		items[key] = append(items[key], v.HumanTier)
	}
	percent := func(n, d int) float64 {
		if d == 0 {
			return 0
		}
		return 100 * float64(n) / float64(d)
	}
	fmt.Fprintf(b, "Count: %d (latest label per reviewer/item/live model); agreement: %.1f%%.\n\nWrong-detection rate: %.1f%% (%d/%d).\n\nInter-reviewer agreement: %.1f%% (%d/%d pairs).\n\n", len(latest), percent(agreed, len(latest)), percent(wrong, len(latest)), wrong, len(latest), percent(same, pairs), same, pairs)
	fmt.Fprint(b, "Model-vs-human tier confusion matrix (rows: model, columns: human; 0 = dismissed):\n\n| Model | 0 | P1 | P2 | P3 | P4 |\n|---|---:|---:|---:|---:|---:|\n")
	for i, row := range matrix {
		fmt.Fprintf(b, "| P%d | %d | %d | %d | %d | %d |\n", i+1, row[0], row[1], row[2], row[3], row[4])
	}
	fmt.Fprint(b, "\nPer-class agreement (review tier agreement for each distinct detected class):\n\n| Class | Count | Agreement |\n|---|---:|---:|\n")
	names := []string{}
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := classes[name]
		safe := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(name)
		fmt.Fprintf(b, "| %s | %d | %.1f%% |\n", safe, c.Count, percent(c.Agreed, c.Count))
	}
	fmt.Fprintln(b)
}
