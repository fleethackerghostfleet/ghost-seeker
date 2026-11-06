//go:build !prod

package main

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestDemoReviewSeedStartupAndReset(t *testing.T) {
	data := store.New(100)
	fillBacklog(data, sensor.NewGenerator(), 10)
	assertSeed := func() {
		t.Helper()
		items, _, _, _ := data.Snapshot()
		count := 0
		for _, item := range items {
			if !item.ReviewFirst {
				continue
			}
			count++
			if item.Img == "" || len(item.Dets) != 1 || item.Dets[0].Class != "marine mammal" || item.Reviewed {
				t.Fatalf("invalid first review item: %+v", item)
			}
		}
		if count != 1 {
			t.Fatalf("want one first review item, got %d", count)
		}
	}
	assertSeed()
	if _, _, ok := seedReviewAfterReset(data, data.Epoch(), time.Now()); ok {
		t.Fatal("reseeded without reset")
	}
	for epoch := uint64(0); epoch < 2; epoch++ {
		data.Reset()
		if _, next, ok := seedReviewAfterReset(data, epoch, time.Now()); !ok || next != epoch+1 {
			t.Fatal("reset did not seed review")
		}
		assertSeed()
	}
}
