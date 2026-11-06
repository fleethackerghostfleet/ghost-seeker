//go:build !prod

package sensor

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestLowValueFramesAreKeptAtP4InsteadOfDropped(t *testing.T) {
	g := NewGenerator()
	lowValue := 0
	for i := 0; i < 500; i++ {
		item, _ := g.Tick(&catalog.Sources[i%len(catalog.Sources)], time.Now(), "maritime", nil)
		if item == nil {
			t.Fatal("Tick dropped a frame; every frame should reach the feed")
		}
		if item.Dets == nil {
			t.Fatal("empty frame has nil detections; the feed needs an empty list")
		}
		if (item.Edge < EdgeKeep) != (item.Filtered == command.FilteredLowValue) {
			t.Fatalf("edge %.2f filtered %q", item.Edge, item.Filtered)
		}
		if item.Filtered != "" {
			lowValue++
			if item.Tier != 4 {
				t.Fatalf("low-value tier = %d, want P4", item.Tier)
			}
		}
	}
	if lowValue == 0 {
		t.Fatal("no low-value frames in 500 ticks")
	}
}

func TestRepeatIsAnIndependentP4CopyOfTheOriginal(t *testing.T) {
	g := NewGenerator()
	var item *command.Item
	for item == nil || item.Filtered != "" || len(item.Dets) == 0 {
		item, _ = g.Tick(&catalog.Sources[0], time.Now(), "maritime", nil)
	}
	repeat := g.Repeat(item, "maritime", nil)
	if repeat.ID == item.ID || repeat.DupOf != item.ID || repeat.Filtered != command.FilteredDuplicate || repeat.Img != item.Img {
		t.Fatalf("repeat = %+v", repeat)
	}
	if repeat.Tier != 4 || repeat.CentralTier != item.CentralTier {
		t.Fatalf("repeat tier %d central %d, original central %d", repeat.Tier, repeat.CentralTier, item.CentralTier)
	}
	repeat.Dets[0].Class = "changed"
	if item.Dets[0].Class == "changed" {
		t.Fatal("repeat shares detections with the original")
	}
}
