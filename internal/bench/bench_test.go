package bench

import (
	"math"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
)

func TestNDCG(t *testing.T) {
	if got := NDCG([]int{1, 2, 3, 4}, 4); got != 1 {
		t.Fatalf("ideal order NDCG = %.3f, want 1", got)
	}
	want := (Gain(4) + Gain(1)/math.Log2(3)) / Gain(1)
	if got := NDCG([]int{4, 1}, 2); math.Abs(got-want) > 1e-12 {
		t.Fatalf("NDCG = %.6f, want %.6f", got, want)
	}
}

func TestBurden(t *testing.T) {
	if b, ok := Burden([]int{1, 3, 1, 4}); !ok || b != .75 {
		t.Fatalf("burden = %.2f %v, want .75 true", b, ok)
	}
	if _, ok := Burden([]int{2, 3}); ok {
		t.Fatal("window without P1 should not count")
	}
}

func TestFrameTierIgnoresFalsePositives(t *testing.T) {
	dets := []command.Detection{{Class: "cargo ship"}, {Class: "submarine"}}
	if got := FrameTier("maritime", dets, []bool{false, true}); got != 4 {
		t.Fatalf("tier = %d, want 4 when the submarine is a false positive", got)
	}
}

func TestGenerateDeterministic(t *testing.T) {
	a, b := Generate(7, 50), Generate(7, 50)
	for i := range a {
		if a[i].Src != b[i].Src || len(a[i].Dets) != len(b[i].Dets) || a[i].Edge != b[i].Edge {
			t.Fatalf("frame %d differs between runs", i)
		}
	}
}

// TestModelsMeetFloor guards the held-out benchmark against regressions in scoring or weights.
func TestModelsMeetFloor(t *testing.T) {
	frames := Generate(101, 4000)
	for model := range Missions {
		r := Evaluate(model, frames)
		if r.CriticalMiss > .01 || r.NDCG < .95 || r.P1Recall < .85 || r.EdgeMissP1 > .01 {
			t.Errorf("%s below floor: critical miss %.3f, NDCG %.3f, P1 recall %.3f, edge miss P1 %.3f", model, r.CriticalMiss, r.NDCG, r.P1Recall, r.EdgeMissP1)
		}
	}
}
