package sensor

import (
	"math/rand"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestUnknownEdgeAndAttributes(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		a := Attributes(rng, "unknown")
		kind := "persistent"
		if a.Transient {
			kind = "transient"
		} else if a.Approaching {
			kind = "approaching"
		}
		seen[kind] = true
		d := command.Detection{Class: "unknown", Conf: .01, Attrs: a}
		score := EdgeScore(rng, []command.Detection{d})
		if a.Transient && score >= EdgeKeep || !a.Transient && score < EdgeKeep {
			t.Fatalf("edge score %v for %+v", score, a)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing unknown variants: %v", seen)
	}
	if got := EdgeScore(rng, nil); got != 0 {
		t.Fatalf("empty edge score: %v", got)
	}
}

func TestTickInjectsUnknownSeparately(t *testing.T) {
	g := NewGenerator()
	seen := false
	for _, source := range catalog.Sources {
		for i := 0; i < 200; i++ {
			item, _ := g.Tick(&source, time.Now(), "maritime", nil)
			if item == nil {
				continue
			}
			for _, d := range item.Dets {
				if d.Class != "unknown" {
					continue
				}
				seen = true
				b := d.Box
				if b.X < 0 || b.Y < 0 || b.W <= 0 || b.H <= 0 || b.X+b.W > 480 || b.Y+b.H > 300 {
					t.Fatalf("unknown box out of bounds: %+v", b)
				}
			}
		}
	}
	if !seen {
		t.Fatal("no injected unknown contacts")
	}
}
