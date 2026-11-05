package ingest

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

func TestReplaySceneCadence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "000001.jpg"), frameJPEG(1), 0600); err != nil {
		t.Fatal(err)
	}
	var set FrameSet
	if err := json.Unmarshal([]byte(`{"scene_every_s":4,"source":"ocean"}`), &set); err != nil {
		t.Fatal(err)
	}
	set.Path = dir
	clk := clock.NewVirtual(time.Now())
	r, err := NewReplay(clk, time.Second, set, FrameSet{Path: dir, Source: "ship"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.queue {
		if c.Source == "ocean" && (c.SceneEvery == nil || *c.SceneEvery != 4*time.Second) {
			t.Fatalf("cadence not carried: %+v", c)
		}
		if c.Source == "ship" && c.SceneEvery != nil {
			t.Fatal("ship override must stay unset")
		}
	}
	for _, seconds := range []float64{-1, math.NaN(), math.Inf(1), 1e12} {
		set.SceneEveryS = &seconds
		if _, err := NewReplay(clk, time.Second, set); err == nil {
			t.Fatalf("accepted invalid cadence %v", seconds)
		}
	}
	zero := 0.0
	set.SceneEveryS = &zero
	empty := t.TempDir()
	set.Path = empty
	r, err = NewReplay(clk, time.Second, set)
	if err != nil || len(r.queue) != 0 {
		t.Fatalf("empty replay: %v %v", r, err)
	}
}

func TestPerSourceSceneCadence(t *testing.T) {
	an := &scripted{empty: map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true}}
	g, _, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Minute
	short := 4 * time.Second
	ingest := func(source string, override *time.Duration, want bool) {
		t.Helper()
		c := capture(clk, 1)
		c.Source, c.SceneEvery = source, override
		row, err := g.Ingest(context.Background(), c)
		if err != nil || (row != nil) != want {
			t.Fatalf("%s: row=%v err=%v want new=%v", source, row, err, want)
		}
		if row != nil && row.Item.Features.Tags["event"] != "scene" {
			t.Fatalf("not a scene: %+v", row.Item)
		}
	}
	ingest("ocean", &short, true)
	ingest("ship", nil, true)
	clk.Advance(3 * time.Second)
	ingest("ocean", &short, false)
	clk.Advance(time.Second)
	ingest("ocean", &short, true)
	ingest("ship", nil, false)
	clk.Advance(time.Minute)
	ingest("ship", nil, true)
	zero := time.Duration(0)
	if g.sceneEvery(&Capture{SceneEvery: &zero}) != 0 || g.sceneEvery(&Capture{}) != time.Minute {
		t.Fatal("zero override/global fallback changed")
	}
	g.cfg.SceneEvery = 0
	ingest("override-with-global-disabled", &short, true)
}
