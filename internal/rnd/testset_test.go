package rnd

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
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
	one := []Prediction{{EdgePriority: 0, Detections: []Detection{{Label: "boat"}}}}
	Rank(one)
	if one[0].ModelTier != 1 {
		t.Fatal(one)
	}
	ps := make([]Prediction, 500)
	for i := range ps {
		ps[i].EdgePriority = float64(500 - i)
		ps[i].Detections = []Detection{{Label: "boat"}}
	}
	Rank(ps)
	counts := [4]int{}
	for _, p := range ps {
		counts[p.ModelTier-1]++
	}
	if counts != [4]int{50, 125, 175, 150} {
		t.Fatal(counts)
	}
	rev := []Prediction{{ID: "low", EdgePriority: .1, Detections: []Detection{{Label: "boat"}}}, {ID: "high", EdgePriority: .9, Detections: []Detection{{Label: "boat"}}}}
	Rank(rev)
	if rev[1].ModelTier != 1 || rev[0].ModelTier != 4 {
		t.Fatal("tiers must follow scores, not input order", rev)
	}
	ties := []Prediction{}
	for _, id := range []string{"z", "a", "b", "c"} {
		ties = append(ties, Prediction{ID: id, EdgePriority: 1, Detections: []Detection{{Label: "boat"}}})
	}
	Rank(ties)
	for i, tier := range []int{1, 3, 4, 4} {
		if ties[i].ModelTier != tier {
			t.Fatal(ties)
		}
	}
	Rank(ties)
	if ties[0].ID != "z" || ties[0].ModelTier != 1 {
		t.Fatal("ties must retain input order, not ID order", ties)
	}
}

func TestRankScenes(t *testing.T) {
	scenes := []Prediction{{EdgePriority: 100}, {EdgePriority: .2, Detections: []Detection{}}}
	Rank(scenes)
	for _, p := range scenes {
		if p.ModelTier != 4 {
			t.Fatal(scenes)
		}
	}
	ps := make([]Prediction, 500)
	for i := range ps {
		ps[i].EdgePriority = .2
		if i < 185 {
			ps[i].Detections = []Detection{{Label: "boat"}}
			ps[i].EdgePriority = float64(500 - i)
		}
	}
	Rank(ps)
	counts := [4]int{}
	for i, p := range ps {
		if i >= 185 && p.ModelTier != 4 {
			t.Fatal("scene ranked", i, p)
		}
		counts[p.ModelTier-1]++
	}
	if counts != [4]int{18, 46, 65, 371} {
		t.Fatal(counts)
	}
}

func TestRerank(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "predictions.jsonl")
	rows := []Prediction{{ID: "scene", ModelTier: 1, EdgePriority: .2, ModelScore: .2, ModelID: "real", LatencyMS: 15}, {ID: "contact", ModelTier: 4, EdgePriority: .8, ModelScore: .8, Detections: []Detection{{Label: "boat", Conf: .9}}}}
	if err := writeJSONL(path, rows); err != nil {
		t.Fatal(err)
	}
	if err := Rerank(dir); err != nil {
		t.Fatal(err)
	}
	got, err := readRows[Prediction](path)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].ModelTier = 4
	rows[1].ModelTier = 1
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("inference changed: %v", got)
	}
	if err = Rerank(t.TempDir()); err == nil {
		t.Fatal("missing predictions accepted")
	}
	if err = os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = Rerank(dir); err == nil {
		t.Fatal("empty predictions accepted")
	}
	if err = os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = Rerank(dir); err == nil {
		t.Fatal("corrupt predictions accepted")
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
	if len(engine.Scorers) != 7 {
		t.Fatal("live engine changed")
	}
}
func TestBuildMissingSources(t *testing.T) {
	if err := Build(context.Background(), t.TempDir(), "unused", nil); err == nil {
		t.Fatal("accepted missing sources")
	}
}

func TestSelectUnique(t *testing.T) {
	frames := []frame{}
	for _, value := range []string{"black", "left", "black", "right", "black"} {
		frames = append(frames, frame{path: value, read: func() ([]byte, error) { return []byte(value), nil }})
	}
	seen := map[[32]byte]bool{}
	f, _, _, err := selectUnique(frames, 0, seen)
	if err != nil || f.path != "black" {
		t.Fatal(f, err)
	}
	f, _, _, err = selectUnique(frames, 2, seen)
	if err != nil || f.path != "right" {
		t.Fatal("equal distance must prefer next frame", f, err)
	}
	f, _, _, err = selectUnique(frames, 4, seen)
	if err != nil || f.path != "left" {
		t.Fatal("nearest available earlier frame", f, err)
	}
	if _, _, _, err = selectUnique(frames, 0, seen); err == nil {
		t.Fatal("duplicate exhaustion accepted")
	}
	if _, _, _, err = selectUnique(nil, 0, seen); err == nil {
		t.Fatal("empty clip accepted")
	}
	otherClipSeen := map[[32]byte]bool{sha256.Sum256([]byte("black")): true}
	f, _, _, err = selectUnique(frames, 0, otherClipSeen)
	if err != nil || f.path != "left" {
		t.Fatal("cross-clip duplicate not skipped", f, err)
	}
	failure := errors.New("read failed")
	bad := []frame{{read: func() ([]byte, error) { return nil, failure }}}
	if _, _, _, err = selectUnique(bad, 0, map[[32]byte]bool{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
