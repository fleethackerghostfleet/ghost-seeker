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
		if err := CheckFloor(r); err != nil {
			t.Error(err)
		}
	}
}

func TestSmallRunStillRanks(t *testing.T) {
	if r := Evaluate("maritime", Generate(5, 120)); r.NDCG == 0 {
		t.Fatal("fewer kept frames than a window should still report NDCG")
	}
}
