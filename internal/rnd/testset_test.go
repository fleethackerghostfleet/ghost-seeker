package rnd

import (
	"context"
	"reflect"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestAllocate(t *testing.T) {
	counts, err := Allocate([]int{5, 100, 100}, 150)
	if err != nil || !reflect.DeepEqual(counts, []int{5, 73, 72}) {
		t.Fatalf("%v %v", counts, err)
	}
	caps := make([]int, 10)
	for i := range caps {
		caps[i] = 100
	}
	counts, err = Allocate(caps, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range counts {
		if n != 50 {
			t.Fatal(counts)
		}
	}
	for _, caps := range [][]int{nil, {0}, {1, 2}, {-1, 1000}} {
		if _, err = Allocate(caps, 500); err == nil {
			t.Fatalf("accepted %v", caps)
		}
	}
	if _, err = Allocate([]int{10}, 0); err == nil {
		t.Fatal("accepted empty sample")
	}
}
func TestSampleNaturalOrder(t *testing.T) {
	if !naturalLess("f2.jpg", "f10.jpg") || naturalLess("f10.jpg", "f2.jpg") || naturalLess("f2.jpg", "f2.jpg") {
		t.Fatal("natural order")
	}
	for _, n := range []int{50, 51, 999} {
		last := -1
		for i := 0; i < 50; i++ {
			idx := SampleIndex(i, n, 50)
			if idx <= last {
				t.Fatal("repeat")
			}
			last = idx
		}
		if last != n-1 || SampleIndex(0, n, 50) != 0 {
			t.Fatal("endpoints")
		}
	}
	if SampleIndex(0, 5, 1) != 2 {
		t.Fatal("singleton midpoint")
	}
}
func TestRank(t *testing.T) {
	Rank(nil)
	one := []Prediction{{EdgePriority: 0}}
	Rank(one)
	if one[0].ModelTier != 1 {
		t.Fatal(one)
	}
	ps := make([]Prediction, 500)
	for i := range ps {
		ps[i].EdgePriority = float64(500 - i)
	}
	Rank(ps)
	counts := [4]int{}
	for _, p := range ps {
		counts[p.ModelTier-1]++
	}
	if counts != [4]int{50, 125, 175, 150} {
		t.Fatal(counts)
	}
	ties := []Prediction{{EdgePriority: 1}, {EdgePriority: 1}, {EdgePriority: 1}, {EdgePriority: 1}}
	Rank(ties)
	if ties[0].ModelTier != 1 || ties[3].ModelTier != 4 {
		t.Fatal(ties)
	}
}
func TestPrediction(t *testing.T) {
	empty := Predict("empty", &pb.AnalyzeResponse{})
	if empty.ModelScore != .2 || empty.AltTierMaritime != 4 || len(empty.Detections) != 0 {
		t.Fatal(empty)
	}
	r := &pb.AnalyzeResponse{Unknownness: .8, ModelId: "real", LatencyMs: 12, Detections: []*pb.Detection{{Label: "submarine", Confidence: .9, Box: &pb.Box{X: .2, Y: .3, W: .4, H: .5}}}}
	p := Predict("id", r)
	if p.ModelScore != p.EdgePriority || p.ModelScore <= empty.ModelScore || p.ModelID != "real" || p.LatencyMS != 12 || p.Detections[0].Box.W != float64(float32(.4)) {
		t.Fatal(p)
	}
	engine := scoring.Default()
	if len(engine.Scorers) != 5 {
		t.Fatal("live engine changed")
	}
}
func TestBuildMissingSources(t *testing.T) {
	if err := Build(context.Background(), t.TempDir(), "unused", nil); err == nil {
		t.Fatal("accepted missing sources")
	}
}
