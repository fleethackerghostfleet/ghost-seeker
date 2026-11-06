//go:build !prod

package retune

import (
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func TestNegativeDoesNotReplacePositiveLabel(t *testing.T) {
	positive := reviews.Record{ID: "positive", ItemID: "pass-1", ImageID: "image", Reviewer: "A", ModelKey: "maritime", HumanTier: 2, Detections: []command.Detection{{Class: "vessel", Conf: 1}}, ReviewedAt: time.Unix(1, 0)}
	negative := reviews.Record{ID: "negative", ItemID: "pass-2", ImageID: "image", Reviewer: "A", ModelKey: "maritime", DetectorNegative: true, HumanTier: 3, ReviewedAt: time.Unix(2, 0)}
	var r Result
	data, err := prepare([]reviews.Record{positive, negative}, &r)
	if err != nil || r.Usable != 1 || len(data["maritime"]) != 1 || r.DetectorNegatives != 1 || r.MissedDetections != 1 {
		t.Fatalf("result %+v data %+v error %v", r, data, err)
	}
}

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
