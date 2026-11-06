//go:build !prod

package main

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestDemoReviewSeedStartupAndReset(t *testing.T) {
	t.Run("default backfill", func(t *testing.T) { testDemoReviewSeedAutoEscalation(t, 84) })
	t.Run("no backfill", func(t *testing.T) { testDemoReviewSeedAutoEscalation(t, 0) })
}

func testDemoReviewSeedAutoEscalation(t *testing.T, backfill int) {
	data := store.New(100)
	fillBacklog(data, sensor.NewGenerator(), backfill)
	assertSeed := func() {
		t.Helper()
		data.Add(&command.Item{ID: "ordinary-warship", TS: time.Now(), Dets: []command.Detection{{Class: "warship", Conf: .95}}}, 0)
		escalated := false
		for {
			item, _, ok := data.AutoEscalate()
			if !ok {
				break
			}
			if item.ReviewFirst {
				t.Fatal("auto-escalated the human-review seed")
			}
			escalated = escalated || item.ID == "ordinary-warship"
		}
		if !escalated {
			t.Fatal("ordinary warship was not auto-escalated")
		}
		items, _, _, _ := data.Snapshot()
		count := 0
		for _, item := range items {
			if !item.ReviewFirst {
				continue
			}
			count++
			whale := false
			for _, det := range item.Dets {
				whale = whale || det.Class == "marine mammal"
			}
			if item.Img == "" || !whale || item.Reviewed || item.Sent || item.AutoSent {
				t.Fatalf("invalid first review item: %+v", item)
			}
			var frame *catalog.LibraryImage
			for _, image := range catalog.Library[item.SourceID] {
				if image.File == item.Img {
					frame = &image
					break
				}
			}
			if frame == nil || len(item.Dets) != len(frame.Dets) {
				t.Fatalf("seed does not preserve catalog detections: %+v", item)
			}
			for i, det := range item.Dets {
				want := frame.Dets[i]
				if det.Class != want.Cls || det.Box.X != want.B.X || det.Box.Y != want.B.Y || det.Box.W != want.B.W || det.Box.H != want.B.H || det.Ship != want.Ship {
					t.Errorf("detection %d = %+v, want catalog detection %+v", i, det, want)
				}
				if det.Conf < .55 || det.Conf > .98 {
					t.Errorf("detection %d confidence = %v, outside [.55, .98]", i, det.Conf)
				}
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
