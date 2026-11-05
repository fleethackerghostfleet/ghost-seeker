package store

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestFilteredImagesAreStoredAtP4AndCountedAsKeptOnShip(t *testing.T) {
	data := New(10)
	data.Add(&command.Item{ID: "useful", KB: 20}, 2)
	low, _ := data.Add(&command.Item{ID: "low", KB: 20, Filtered: command.FilteredLowValue}, 0)
	repeat, _ := data.Add(&command.Item{ID: "repeat", KB: 20, Filtered: command.FilteredDuplicate, DupOf: "useful"}, 0)
	if low.Tier != 4 || repeat.Tier != 4 {
		t.Fatalf("filtered tiers = %d, %d; want P4", low.Tier, repeat.Tier)
	}
	stats, _ := data.Stats()
	if stats.Cap != 5 || stats.Keep != 1 || stats.Drop != 4 || stats.Bytes != 0 || stats.DB != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if got := len(data.Items()); got != 3 {
		t.Fatalf("retained %d items, want all 3", got)
	}
}

func TestSuppressedThenBoostedImageUsesStoredTierForUpload(t *testing.T) {
	data := New(5)
	data.RestoreDirectives([]command.Directive{{Rules: []command.Rule{
		{Type: "suppress", Classes: []string{"warship"}},
		{Type: "boost", Classes: []string{"warship"}},
	}}})
	data.Add(&command.Item{ID: "low", KB: 100, Manual: 4}, 0)
	item, _ := data.Add(&command.Item{ID: "boosted", KB: 100, Dets: []command.Detection{{Class: "warship", Conf: .9}}}, 0)
	if len(item.Det) != 1 || !item.Det[0].Sup || item.Tier == 4 {
		t.Fatalf("suppressed then boosted image: %+v", item)
	}
	t.Logf("suppressed=%v score=%.4f stored tier=P%d", item.Det[0].Sup, item.Central, item.Tier)
	changed := data.AdvanceUploads(time.Second)
	if len(changed) != 1 || changed[0].ID != "boosted" {
		t.Fatalf("upload order: %+v", changed)
	}
}

func TestEvictionRemovesOldestFilteredImageFirst(t *testing.T) {
	data := New(2)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	data.Add(&command.Item{ID: "useful", TS: start}, 0)
	data.Add(&command.Item{ID: "repeat", TS: start.Add(time.Minute), Filtered: command.FilteredDuplicate}, 0)
	if _, evicted := data.Add(&command.Item{ID: "newest", TS: start.Add(2 * time.Minute)}, 0); evicted != "repeat" {
		t.Fatalf("evicted %q, want the filtered repeat", evicted)
	}
}

func TestAutoEscalationIsMarkedUntilAPersonChangesTheSend(t *testing.T) {
	data := New(5)
	data.Add(&command.Item{ID: "hot", Manual: 1}, 0)
	item, _, ok := data.AutoEscalate()
	if !ok || !item.Sent || !item.AutoSent {
		t.Fatalf("auto escalation = %+v, %v; want sent automatically", item, ok)
	}
	off, on := false, true
	data.PatchItem("hot", nil, nil, nil, &off)
	item, _, _ = data.PatchItem("hot", nil, nil, nil, &on)
	if !item.Sent || item.AutoSent {
		t.Fatalf("operator resend = %+v; want sent by a person", item)
	}
}

func TestHumanReviewPreservesAutomaticSendOrigin(t *testing.T) {
	data := New(5)
	data.Add(&command.Item{ID: "hot", Manual: 1}, 0)
	item, _, _ := data.AutoEscalate()
	if item.HumanReviewed {
		t.Fatal("automatic escalation counted as human review")
	}
	tier := 2
	item, _, _ = data.PatchItem("hot", &tier, nil, nil, nil)
	if !item.HumanReviewed || !item.AutoSent || !item.Sent || item.Tier != 2 {
		t.Fatalf("operator re-rank lost review or send origin: %+v", item)
	}
}

func TestEmptyFrameKeepsEmptyDetectionLists(t *testing.T) {
	data := New(10)
	kept, _ := data.Add(&command.Item{ID: "empty", Filtered: command.FilteredLowValue, Dets: []command.Detection{}}, 0)
	if kept.Dets == nil || kept.Det == nil || data.Items()[0].Dets == nil {
		t.Fatal("empty frame lost its detection lists; the feed renders them as JSON arrays")
	}
}

func TestEvictionKeepsFilteredImagesAPersonDismissed(t *testing.T) {
	data := New(3)
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	data.Add(&command.Item{ID: "useful", TS: start}, 0)
	data.Add(&command.Item{ID: "dismissed", TS: start.Add(time.Minute), Filtered: command.FilteredDuplicate}, 0)
	data.Add(&command.Item{ID: "repeat", TS: start.Add(2 * time.Minute), Filtered: command.FilteredDuplicate}, 0)
	yes := true
	data.PatchItem("dismissed", nil, &yes, &yes, nil)
	if _, evicted := data.Add(&command.Item{ID: "newest", TS: start.Add(3 * time.Minute)}, 0); evicted != "repeat" {
		t.Fatalf("evicted %q, want the untouched repeat", evicted)
	}
}
