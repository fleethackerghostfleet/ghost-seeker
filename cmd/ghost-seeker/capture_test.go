package main

import (
	"math/rand"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestAddCaptureStoresRepeatViewsWithoutChangingFrameCounts(t *testing.T) {
	data, generator, rng := store.New(500), sensor.NewGenerator(), rand.New(rand.NewSource(1))
	repeats, frames := 0, 0
	for i := 0; i < 40; i++ {
		item := &command.Item{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)), Dets: []command.Detection{{Class: "vessel", Conf: .8}}}
		stored, _ := addCapture(data, generator, rng, item, 3, "maritime", nil)
		frames += 4
		for _, capture := range stored[1:] {
			repeats++
			if capture.Filtered != command.FilteredDuplicate || capture.DupOf != item.ID || capture.Tier != 4 {
				t.Fatalf("repeat = %+v", capture)
			}
		}
	}
	stats, _ := data.Stats()
	if repeats == 0 || stats.Cap != frames || stats.Keep != 40 || stats.Drop != frames-40 {
		t.Fatalf("repeats %d stats %+v", repeats, stats)
	}
}
