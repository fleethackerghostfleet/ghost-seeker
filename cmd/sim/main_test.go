package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/scenario"
)

func TestSimulationSmoke(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	rng := rand.New(rand.NewSource(7))
	for y := range 128 {
		for x := range 128 {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001.png"), buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sc := &scenario.Scenario{Name: "smoke", PeriodS: 1, Sets: []ingest.FrameSet{{Path: dir, Source: "mast", Unknownness: .9, Truth: "dolphin"}}}
	trace := link.Trace{Name: "steady", Windows: []link.Window{{Duration: time.Minute, Bps: 9600, RTT: 10 * time.Millisecond}}}
	results := make(map[scheduler.Strategy]*result)
	for _, strategy := range []scheduler.Strategy{scheduler.FIFO, scheduler.Prioritized} {
		r, err := run(strategy, trace, sc, time.Minute, 42, time.Second, runOpts{dedup: "naive"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Ingested != 1 || r.Unknowns != 1 || r.FirstUnknownAnyS < 0 || r.ShoreKnowsAtEnd != 1 || r.BytesOnAir <= 0 || r.Failures != 0 {
			t.Fatalf("%s did not deliver unknown: %+v", strategy, r)
		}
		results[strategy] = r
	}
	fifo, priority := results[scheduler.FIFO], results[scheduler.Prioritized]
	if priority.FirstUnknownAnyS > fifo.FirstUnknownAnyS {
		t.Fatalf("prioritized awareness latency %g exceeds FIFO %g", priority.FirstUnknownAnyS, fifo.FirstUnknownAnyS)
	}
	t.Logf("unknown awareness latency: prioritized=%.3fs FIFO=%.3fs", priority.FirstUnknownAnyS, fifo.FirstUnknownAnyS)
}

func TestSimulationEmptyAndMissingFrames(t *testing.T) {
	trace := link.Trace{Name: "steady", Windows: []link.Window{{Duration: time.Minute, Bps: 9600}}}
	r, err := run(scheduler.Prioritized, trace, &scenario.Scenario{PeriodS: 1}, time.Second, 42, time.Second, runOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Ingested != 0 || r.Unknowns != 0 || r.ShoreKnowsAtEnd != 0 || r.FirstUnknownFactS != -1 {
		t.Fatalf("empty result: %+v", r)
	}
	sc := &scenario.Scenario{PeriodS: 1, Sets: []ingest.FrameSet{{Path: filepath.Join(t.TempDir(), "missing")}}}
	if _, err := run(scheduler.Prioritized, trace, sc, time.Second, 42, time.Second, runOpts{}); !os.IsNotExist(err) {
		t.Fatalf("missing frames: %v", err)
	}
}
