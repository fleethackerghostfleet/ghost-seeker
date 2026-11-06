//go:build !prod

package main

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

// whaleCard reports whether the feed card for item reads as a marine mammal (its top-scoring contact).
func whaleCard(item *command.Item) bool {
	if item == nil || len(item.Dets) == 0 || len(item.Det) != len(item.Dets) || item.Filtered != "" {
		return false
	}
	top := 0
	for i := range item.Det {
		if item.Det[i].Score > item.Det[top].Score {
			top = i
		}
	}
	return item.Dets[top].Class == "marine mammal"
}

func TestDemoBacklogOpensWithVisibleWhale(t *testing.T) {
	data, generator := store.New(500), sensor.NewGenerator()
	fillBacklog(data, generator, 84)
	items, _, _, _ := data.Snapshot()
	// Standing demo policy boosts other classes, which scales every unmatched contact down.
	policy := []command.Directive{{Rules: []command.Rule{{Type: "boost", Classes: []string{"tanker"}}}}}
	for i := range items {
		command.Rescore(&items[i], "maritime", policy)
		if whaleCard(&items[i]) && items[i].Tier == 1 && items[i].FeedFirst {
			return
		}
	}
	t.Fatal("startup backlog has no P1 whale card")
}

func TestDemoWhaleAfterResetAndKeepsComing(t *testing.T) {
	data, generator := store.New(500), sensor.NewGenerator()
	fillBacklog(data, generator, 20)
	whales := newWhaleCadence(data)
	data.Reset()
	item, _ := whales.tick(data, generator, time.Now(), "maritime", nil)
	if stored, _ := data.Add(item, 0); !whaleCard(&stored) || stored.Tier != 1 || !stored.FeedFirst {
		t.Fatalf("first frame after reset is not a visible whale: tier %d", stored.Tier)
	}
	seen := 0
	for i := 0; i < 3*whaleEvery; i++ {
		item, extra := whales.tick(data, generator, time.Now(), "maritime", nil)
		if item != nil {
			stored, _ := data.Add(item, extra)
			if whaleCard(&stored) && stored.Tier <= 2 {
				seen++
			}
		}
	}
	if seen < 3 {
		t.Fatalf("got %d whale frames in %d captures, want >= 3", seen, 3*whaleEvery)
	}
}
