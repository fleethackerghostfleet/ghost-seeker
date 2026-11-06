//go:build !prod

package retune

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestLiveOutcomesExcludedFromFitting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l, err := reviews.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := reviews.Record{ItemID: "i", ModelKey: "live:edge", Model: "Ship score", Reviewer: "A", ModelTier: 2, HumanTier: 2, Detections: []command.Detection{{Class: "ship"}, {Class: "ship"}}, Live: &reviews.LiveProvenance{ShoreItemID: "shore-i"}}
	first, err := l.Append(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Reviewer = "B"
	base.HumanTier = 3
	second, err := l.Append(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Correct(second.ID, "B", "reason", "wrong_detection", "bad box"); err != nil {
		t.Fatal(err)
	}
	base.ItemID = "undone"
	undone, err := l.Append(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Correct(undone.ID, "B", "undo", "", ""); err != nil {
		t.Fatal(err)
	}
	l.Close()
	r, err := Run([]string{path}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.LiveExcluded != 2 || r.Usable != 0 || r.Excluded != 0 {
		t.Fatalf("%+v", r)
	}
	report := r.Markdown()
	for _, want := range []string{"Live reviews excluded from weight fitting: 2", "Count: 2", "agreement: 50.0%", "Wrong-detection rate: 50.0% (1/2)", "Inter-reviewer agreement: 0.0% (0/1 pairs)", "| P2 | 0 | 0 | 1 | 1 | 0 |", "| ship | 2 | 50.0% |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q:\n%s", want, report)
		}
	}
	var empty Result
	emptyReport := empty.Markdown()
	if !strings.Contains(emptyReport, "Count: 0") || strings.Contains(emptyReport, "NaN") {
		t.Fatal(emptyReport)
	}
	first.ModelKey, first.Live = "unknown", nil
	if _, err := prepare([]reviews.Record{first}, &Result{}); err == nil {
		t.Fatal("unknown non-live model must still fail")
	}
}
