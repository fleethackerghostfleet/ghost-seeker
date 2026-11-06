//go:build !prod

package sensor

import (
	"math"
	"math/rand"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

type constantSource struct{ calls int }

func (s *constantSource) Int63() int64 { s.calls++; return 1 << 62 }
func (*constantSource) Seed(int64)     {}

func TestPlacementReturnsGeometryAndBoundsCongestionRetries(t *testing.T) {
	for _, tc := range []struct {
		scene, class string
		draws        int
	}{
		{"harbor", "warship", 3}, {"sky", "drone", 3}, {"aerial", "warship", 4}, {"sat", "warship", 4},
	} {
		t.Run(tc.scene, func(t *testing.T) {
			freeSource := &constantSource{}
			free := placeDetection(rand.New(freeSource), tc.class, tc.scene, .4, nil)
			if freeSource.calls != tc.draws || free.W <= 0 || free.H <= 0 || math.IsNaN(free.X+free.Y) {
				t.Fatalf("free=%+v draws=%d", free, freeSource.calls)
			}
			if free.Top != (tc.scene == "aerial" || tc.scene == "sat") {
				t.Fatalf("top-view=%+v", free)
			}
			if free.Top && (free.L <= 0 || free.B <= 0 || math.Abs(free.CX-free.X-free.W/2) > 1e-9 || math.Abs(free.CY-free.Y-free.H/2) > 1e-9) {
				t.Fatalf("top geometry=%+v", free)
			}
			blockedSource := &constantSource{}
			obstacle := []command.Box{{X: -10000, Y: -10000, W: 20000, H: 20000}}
			blocked := placeDetection(rand.New(blockedSource), tc.class, tc.scene, .4, obstacle)
			if blocked != free || blockedSource.calls != 14*tc.draws || boxesDoNotOverlap(blocked, obstacle) {
				t.Fatalf("congested=%+v draws=%d", blocked, blockedSource.calls)
			}
		})
	}
}

func TestWeightedSelectionEmptySingleAndMultiplePools(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	if weightedPick[string](rng, nil) != "" || weightedPick[int](rng, map[int]float64{}) != 0 {
		t.Fatal("empty pool did not return zero")
	}
	if got := weightedPick(rng, map[string]float64{"boat": 1}); got != "boat" {
		t.Fatalf("single pool=%q", got)
	}
	seen := map[string]bool{}
	for range 1000 {
		got := weightedPick(rng, map[string]float64{"boat": 3, "bird": 7})
		if got != "boat" && got != "bird" {
			t.Fatalf("out-of-pool choice=%q", got)
		}
		seen[got] = true
	}
	if len(seen) != 2 {
		t.Fatalf("positive classes not both selected=%v", seen)
	}
}
