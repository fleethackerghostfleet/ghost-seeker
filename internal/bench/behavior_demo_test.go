//go:build !prod

package bench

import (
	"math"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/sensor"
)

func TestFloorAcceptsBoundariesAndRejectsEachUnsafeMetric(t *testing.T) {
	boundary := Result{Model: "boundary", CriticalMiss: .01, NDCG: .95, P1Recall: .85, EdgeMissP1: .01}
	if err := CheckFloor(boundary); err != nil {
		t.Fatalf("safe boundary rejected: %v", err)
	}
	for _, metric := range []string{"critical", "ranking", "recall", "edge"} {
		t.Run(metric, func(t *testing.T) {
			unsafe := boundary
			switch metric {
			case "critical":
				unsafe.CriticalMiss = math.Nextafter(.01, 1)
			case "ranking":
				unsafe.NDCG = math.Nextafter(.95, 0)
			case "recall":
				unsafe.P1Recall = math.Nextafter(.85, 0)
			case "edge":
				unsafe.EdgeMissP1 = math.Nextafter(.01, 1)
			}
			if err := CheckFloor(unsafe); err == nil || !strings.Contains(err.Error(), "boundary below floor:") {
				t.Fatalf("unsafe %s accepted or wrong error: %v", metric, err)
			}
		})
	}
}

func TestEvaluateEmptyBatchHasFiniteZeroMetrics(t *testing.T) {
	r := Evaluate("maritime", nil)
	if r.Model != "maritime" || r.Frames != 0 || r.Kept != 0 {
		t.Fatalf("empty counts: %+v", r)
	}
	for _, value := range []float64{r.EdgeMissP1, r.EdgeMissP12, r.TierExact, r.TierWithin1, r.P1Recall,
		r.P1Precision, r.CriticalMiss, r.FalseFlash, r.FlashLoad, r.NDCG, r.Burden} {
		if value != 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("empty metric = %v", value)
		}
	}
}

func TestEvaluateThresholdKeepsPartialBatchAndAccountsUnknowns(t *testing.T) {
	frames := []Frame{
		{Src: "EO-P7", Dets: []command.Detection{{Class: "unknown", Conf: 1, Attrs: command.Attributes{Approaching: true}}}, FP: []bool{false}, Edge: sensor.EdgeKeep - .01},
		{Src: "EO-P7", Dets: []command.Detection{{Class: "unknown", Conf: 1}}, FP: []bool{false}, Edge: sensor.EdgeKeep},
		{Src: "EO-P7", Dets: []command.Detection{{Class: "unknown", Conf: 1, Attrs: command.Attributes{Transient: true}}}, FP: []bool{false}, Edge: sensor.EdgeKeep - .01},
	}
	r := Evaluate("maritime", frames)
	if r.Frames != 3 || r.Kept != 1 || math.Abs(r.EdgeDrop-2.0/3) > 1e-12 || r.EdgeMissP1 != 1 || r.EdgeMissP12 != .5 {
		t.Fatalf("partial retention/drop accounting: %+v", r)
	}
	if r.UnknownPersistent != 2 || r.UnknownKept != 1 || r.UnknownP12 != 1 || r.UnknownApproaching != 1 ||
		r.UnknownP1 != 1 || r.UnknownTransient != 1 || r.UnknownP4 != 1 {
		t.Fatalf("unknown accounting: %+v", r)
	}
	if r.TierExact != 1 || r.TierWithin1 != 1 || r.NDCG != 1 || r.Burden != 0 || r.CriticalMiss != 0 {
		t.Fatalf("kept-frame metrics: %+v", r)
	}
}

func TestEvaluateDetectsCriticalMissInOtherwiseValidCandidate(t *testing.T) {
	const key = "p2-unsafe-candidate"
	model := catalog.Models["maritime"]
	model.W = map[string]float64{"submarine": 0}
	catalog.Models[key] = model
	Missions[key] = "maritime"
	t.Cleanup(func() { delete(catalog.Models, key); delete(Missions, key) })
	frames := []Frame{{Src: "EO-P7", Dets: []command.Detection{{Class: "submarine", Conf: 1}}, FP: []bool{false}, Edge: 1}}
	good := Evaluate("maritime", frames)
	if good.P1Recall != 1 || good.CriticalMiss != 0 || good.TierExact != 1 {
		t.Fatalf("valid candidate control: %+v", good)
	}
	bad := Evaluate(key, frames)
	if bad.Kept != 1 || bad.P1Recall != 0 || bad.CriticalMiss != 1 || bad.TierExact != 0 || bad.TierWithin1 != 0 {
		t.Fatalf("unsafe candidate: %+v", bad)
	}
	if err := CheckFloor(bad); err == nil {
		t.Fatal("unsafe candidate passed safety floor")
	}
}

func TestRankingHandlesEmptyRelevanceAndDistinguishesRealOrderings(t *testing.T) {
	for _, tc := range []struct {
		tiers []int
		k     int
	}{{nil, 20}, {[]int{4, 4}, 20}, {[]int{1, 4}, 0}} {
		if got := NDCG(tc.tiers, tc.k); got != 1 {
			t.Fatalf("zero-relevance NDCG(%v,%d) = %v", tc.tiers, tc.k, got)
		}
	}
	if good, bad := NDCG([]int{1, 4}, 1), NDCG([]int{4, 1}, 1); good != 1 || bad != 0 {
		t.Fatalf("top-one order controls: good=%v bad=%v", good, bad)
	}
}

func TestGeneratorSkipsUnavailableSourcesAndReturnsCompleteRequestedBatch(t *testing.T) {
	oldSources, oldLibrary := catalog.Sources, catalog.Library
	t.Cleanup(func() { catalog.Sources, catalog.Library = oldSources, oldLibrary })
	var ready catalog.Source
	var image catalog.LibraryImage
	for _, src := range oldSources {
		if images := oldLibrary[src.ID]; len(images) > 0 && len(images[0].Dets) > 0 {
			ready, image = src, images[0]
			break
		}
	}
	if ready.ID == "" {
		t.Fatal("missing embedded image fixture")
	}
	unavailable := ready
	unavailable.ID = "unavailable"
	catalog.Sources = []catalog.Source{unavailable, ready}
	catalog.Library = map[string][]catalog.LibraryImage{ready.ID: {image}}
	frames := GenerateWithUnknowns(17, 32, false)
	if len(frames) != 32 {
		t.Fatalf("partial library returned %d frames, want 32", len(frames))
	}
	falsePositives := 0
	for _, frame := range frames {
		if frame.Src != ready.ID || len(frame.FP) != len(frame.Dets) || len(frame.Dets) < len(image.Dets) || len(frame.Dets) > len(image.Dets)+1 {
			t.Fatalf("invalid generated frame: %+v", frame)
		}
		for i, detection := range image.Dets {
			if frame.Dets[i].Class != detection.Cls || frame.FP[i] {
				t.Fatalf("image contacts were not preserved: %+v", frame)
			}
		}
		if len(frame.Dets) > len(image.Dets) {
			if !frame.FP[len(image.Dets)] {
				t.Fatal("injected false positive not marked")
			}
			falsePositives++
		}
	}
	if falsePositives == 0 || falsePositives == len(frames) {
		t.Fatalf("need both injected and non-injected controls: %d", falsePositives)
	}
}

func TestGeneratorWithoutLibraryReturnsNoSyntheticFrames(t *testing.T) {
	old := catalog.Library
	t.Cleanup(func() { catalog.Library = old })
	catalog.Library = nil
	if got := GenerateWithUnknowns(17, 32, true); got != nil {
		t.Fatalf("empty library generated frames: %+v", got)
	}
}
