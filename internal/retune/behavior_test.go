package retune

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestModelKeyHonorsExplicitKeysAndLegacyNames(t *testing.T) {
	for _, tc := range []struct {
		record reviews.Record
		want   string
	}{
		{reviews.Record{ModelKey: "maritime", Model: "ignored legacy name"}, "maritime"},
		{reviews.Record{ModelKey: "unknown mission", Model: "maritime"}, ""},
		{reviews.Record{Model: "maritime"}, "maritime"},
		{reviews.Record{Model: catalog.Models["maritime"].Name}, "maritime"},
		{reviews.Record{Model: "unknown mission"}, ""},
		{reviews.Record{}, ""},
	} {
		if got := ModelKey(tc.record); got != tc.want {
			t.Fatalf("ModelKey(%+v)=%q want=%q", tc.record, got, tc.want)
		}
	}
}

func TestMeasureCountsCriticalMissesAndMissingP1Labels(t *testing.T) {
	const key = "test-retune-metrics"
	model := catalog.Models["maritime"]
	model.W = map[string]float64{"warship": 1, "debris": 0}
	catalog.Models[key] = model
	t.Cleanup(func() { delete(catalog.Models, key) })
	examples := []Example{
		{Human: 1, Dets: []command.Detection{{Class: "warship", Conf: 1}}},
		{Human: 1, Dets: []command.Detection{{Class: "debris", Conf: 1}}},
		{Human: 2, Dets: []command.Detection{{Class: "debris", Conf: 1}}},
		{Human: 3, Dets: []command.Detection{{Class: "debris", Conf: 1}}},
		{Human: 4, Dets: []command.Detection{{Class: "debris", Conf: 1}}},
	}
	if got := Measure(key, examples); got != (Metrics{Count: 5, Exact: .4, Within1: .6, P1Recall: .5, CriticalMiss: 2}) {
		t.Fatalf("mixed metrics=%+v", got)
	}
	if got := objective(key, examples); got != 13 {
		t.Fatalf("critical-miss weighted objective=%v", got)
	}
	if got := Measure(key, nil); got != (Metrics{}) {
		t.Fatalf("empty metrics=%+v", got)
	}
	if got := Measure(key, examples[2:]); got.Count != 3 || got.P1Recall != 0 {
		t.Fatalf("no-P1 metrics=%+v", got)
	}
}

func TestRunReturnsReadCountsButNoProposalForInvalidReview(t *testing.T) {
	record := reviews.Record{ID: "invalid-tier", ItemID: "image", Reviewer: "watch", ModelKey: "maritime", HumanTier: 9,
		Detections: []command.Detection{{Class: "debris", Conf: 1}}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reviews.jsonl")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Run([]string{path}, 1)
	if err == nil || !strings.Contains(err.Error(), "invalid item, tier or detections") {
		t.Fatalf("invalid tier=%v", err)
	}
	if r.Raw != 1 || r.Effective != 1 || len(r.Models) != 0 || len(r.Weights) != 0 {
		t.Fatalf("partial result=%+v", r)
	}
}

func TestMarkdownEmptyPartialAndGateOutcomes(t *testing.T) {
	empty := (Result{}).Markdown()
	if !strings.Contains(empty, "Count: 0") || strings.Contains(empty, "NaN") || strings.Contains(empty, "Inf") {
		t.Fatalf("empty report=%s", empty)
	}
	r := Result{Raw: 3, Effective: 2, Usable: 1, Reviewers: map[string]int{"z\nwatch": 1, "alpha": 2}, Models: []ModelResult{
		{Key: "too-little", Accepted: true, TooLittle: true},
		{Key: "rejected", Gate: []Gate{{Seed: 101, Error: "threshold violation"}}},
		{Key: "accepted", Accepted: true, Changes: []Change{{Class: "debris", Before: .2, After: .3, Count: 10}}, Gate: []Gate{{Seed: 102}}},
	}}
	text := r.Markdown()
	for _, want := range []string{"Records: 3 unique log events; 2 effective reviews; 1 usable labels", "Too little data", "FAIL: threshold violation", "**REJECTED**", "**ACCEPTED for manual review only.**", "| debris | 10 | 0.20 | 0.30 |", "| 102 | 0.000 | 0.000 | 0.000 | 0.000 | PASS |", "- z watch: 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in report:\n%s", want, text)
		}
	}
	if strings.Index(text, "- alpha: 2") > strings.Index(text, "- z watch: 1") {
		t.Fatal("reviewers not sorted")
	}
}

func TestLiveReportDeduplicatesClassesAndKeepsNewestReviewerLabel(t *testing.T) {
	base := reviews.Record{ID: "a", ItemID: "image", Reviewer: "A", ModelKey: "live:camera", ModelTier: 2, HumanTier: 1, ReviewedAt: time.Unix(1, 0),
		Detections: []command.Detection{{Class: "boat|night\nwatch"}, {Class: "boat|night\nwatch"}}}
	latest := base
	latest.ID = "z"
	latest.HumanTier = 2
	latest.Reason = "wrong_detection"
	other := latest
	other.ID = "other"
	other.Reviewer = "B"
	r := Result{LiveRecords: []reviews.Record{latest, base, other}}
	text := r.Markdown()
	for _, want := range []string{"Count: 2 (latest label per reviewer/item/live model); agreement: 100.0%", "Wrong-detection rate: 100.0% (2/2)", "Inter-reviewer agreement: 100.0% (1/1 pairs)", "| boat\\|night watch | 2 | 100.0% |"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in live report:\n%s", want, text)
		}
	}
}
