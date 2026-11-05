package retune

import (
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"strings"
	"testing"
)

func TestNegativeExclusion(t *testing.T) {
	var r Result
	data, err := prepare([]reviews.Record{
		{ID: "a", ItemID: "a", DetectorNegative: true, HumanTier: 2},
		{ID: "b", ItemID: "b", DetectorNegative: true, HumanTier: 4},
	}, &r)
	if err != nil || len(data) != 0 || r.Usable != 0 || r.DetectorNegatives != 2 || r.MissedDetections != 1 {
		t.Fatalf("%+v %v %v", r, data, err)
	}
	if !strings.Contains(r.Markdown(), "P1–P3): 1 — excluded from weight fitting") {
		t.Fatal(r.Markdown())
	}
	if !strings.HasPrefix(r.Markdown(), "# Human review retuning proposal\n\nRecords:") {
		t.Fatal(r.Markdown())
	}
}
