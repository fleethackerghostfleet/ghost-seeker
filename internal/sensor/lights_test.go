package sensor

import (
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

func TestNightFramesSeparateFromBenchLibrary(t *testing.T) {
	count := 0
	for _, images := range catalog.Library {
		for _, image := range images {
			if !image.Background {
				count++
			}
			for _, nights := range catalog.NightLibrary {
				for _, night := range nights {
					if image.File == night.File {
						t.Fatal("night frame leaked into bench library")
					}
				}
			}
		}
	}
	if count != 48 {
		t.Fatalf("ordinary library count = %d", count)
	}
	g := NewGenerator()
	seen := false
	for i := 0; i < 500; i++ {
		item, _ := g.Tick(&catalog.Sources[0], time.Now(), "maritime", nil)
		if item == nil || item.Night == nil {
			continue
		}
		seen = true
		if !*item.Night || len(item.Dets) == 0 {
			t.Fatal("incomplete night item")
		}
		item.Dets[0].Lights = nil
		break
	}
	if !seen {
		t.Fatal("no night sample in 500 source-forced ticks")
	}
}
