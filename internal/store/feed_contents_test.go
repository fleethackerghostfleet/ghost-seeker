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
	if stats.Cap != 5 || stats.Keep != 1 || stats.Drop != 4 || stats.Bytes != 20 || stats.DB != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if got := len(data.Items()); got != 3 {
		t.Fatalf("retained %d items, want all 3", got)
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
