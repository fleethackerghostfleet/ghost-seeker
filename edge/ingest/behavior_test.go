package ingest

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

func TestFrameLogIngestNewAndHashFold(t *testing.T) {
	an := &scripted{}
	g, _, clk := newIngester(t, DedupHash, an)
	g.Frames = NewFrameLog(4)
	c := capture(clk, 40)
	c.CapturedAt = time.Time{}
	row, err := g.Ingest(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Ingest(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	events, counts, raw := g.Frames.Since(0, 10)
	if len(events) != 2 || events[0].Outcome != "new" || events[1].Outcome != "folded" || counts["new"] != 1 || counts["folded"] != 1 || raw != int64(2*len(c.Data)) || an.calls != 1 {
		t.Fatalf("events=%+v counts=%v raw=%d calls=%d", events, counts, raw, an.calls)
	}
	for _, e := range events {
		if e.ItemID != row.Item.Id || e.Source != "cam-a" || e.Label != "boat" || e.AtUnixMs != clk.Now().UnixMilli() {
			t.Fatalf("event=%+v", e)
		}
		if _, err := jpeg.Decode(bytes.NewReader(g.Frames.Thumb(e.Seq))); err != nil {
			t.Fatalf("thumbnail: %v", err)
		}
	}
	counts["new"] = 999
	_, again, _ := g.Frames.Since(0, 0)
	if again["new"] != 1 || g.Frames.Folds(row.Item.Id) != 1 || g.Frames.Thumb(99) != nil {
		t.Fatalf("mutable counters=%v", again)
	}
}

func TestFrameLogSightingsOutcomes(t *testing.T) {
	an := &scripted{script: map[int][]*pb.Sighting{
		1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "tug")},
		2: {sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "tug")},
		3: {sighting("s1", pb.Sighting_EVENT_UPDATE, false, "tanker")},
		4: {sighting("s1", pb.Sighting_EVENT_CLOSE, false, "tanker")},
	}}
	g, _, clk := newIngester(t, DedupSightings, an)
	g.Frames = NewFrameLog(10)
	for i := 0; i < 4; i++ {
		if _, err := g.Ingest(context.Background(), capture(clk, uint8(40+i))); err != nil {
			t.Fatal(err)
		}
	}
	events, _, _ := g.Frames.Since(0, 10)
	want := []string{"new", "folded", "updated", "closed"}
	if len(events) != len(want) {
		t.Fatalf("events=%+v", events)
	}
	for i, e := range events {
		if e.Outcome != want[i] || e.ItemID == "" {
			t.Fatalf("event %d=%+v want=%s", i, e, want[i])
		}
	}
	if events[1].ItemID != events[0].ItemID || events[1].Label != "boat" || g.Frames.Folds(events[0].ItemID) != 1 {
		t.Fatalf("fold=%+v", events[1])
	}
}

func TestFrameLogErrorDroppedSceneAndEmptyWater(t *testing.T) {
	g, _, clk := newIngester(t, DedupNaive, &scripted{})
	g.Frames = NewFrameLog(10)
	c := capture(clk, 45)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Ingest(ctx, c); err == nil {
		t.Fatal("canceled ingest succeeded")
	}
	g.logFrame(c, nil, nil, g.Deduped(), g.Count)
	g.Dups++
	g.lastResp = &pb.AnalyzeResponse{}
	g.logFrame(c, nil, nil, 0, g.Count)
	row := &store.Row{Item: &pb.Item{Id: "scene-item", Features: &pb.Features{Tags: map[string]string{"event": "scene"}}}}
	g.logFrame(&Capture{Data: []byte("undecodable"), Source: "cam-a", CapturedAt: clk.Now()}, row, nil, g.Deduped(), g.Count)
	events, _, _ := g.Frames.Since(0, 10)
	if len(events) != 4 || events[0].Outcome != "error" || events[1].Outcome != "dropped" || events[2].Outcome != "folded" || events[2].Label != "empty water" || events[3].Label != "scene" || events[3].thumb != nil {
		t.Fatalf("events=%+v", events)
	}
}

func TestReadBoundedValidImageBoundary(t *testing.T) {
	data := frameJPEG(50)
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{int64(len(data)), int64(len(data) + 1)} {
		got, err := readBounded(path, limit)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("limit=%d len=%d err=%v", limit, len(got), err)
		}
	}
	if _, err := readBounded(path, int64(len(data)-1)); err == nil || !strings.Contains(err.Error(), "file exceeds byte cap") {
		t.Fatalf("overflow=%v", err)
	}
	if _, err := readBounded(path+".missing", 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing=%v", err)
	}
	if _, err := readBounded(filepath.Dir(path), 100); err == nil {
		t.Fatal("directory read succeeded")
	}
}

func TestLiveValidImageByteCapAndPushAdmission(t *testing.T) {
	q := runningQueue(t, 1)
	data := frameJPEG(60)
	h := q.Handler(liveTestToken, "", int64(len(data)))
	if w := requestFrame(h, data, "image/jpeg", "?source=cam-a", liveTestToken); w.Code != 202 {
		t.Fatalf("at limit: %d %s", w.Code, w.Body.String())
	}
	h = q.Handler(liveTestToken, "", int64(len(data)-1))
	if w := requestFrame(h, data, "image/jpeg", "?source=cam-a", liveTestToken); w.Code != 413 {
		t.Fatalf("overflow: %d %s", w.Code, w.Body.String())
	}
	full := NewQueue(1)
	if !full.reserve() {
		t.Fatal("initial reservation failed")
	}
	if _, err := full.Push(context.Background(), &Capture{Source: "cam-a"}, "local"); !errors.Is(err, ErrBusy) {
		t.Fatalf("full queue=%v", err)
	}
	<-full.slots
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := full.Push(ctx, &Capture{Source: "cam-a"}, "local"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled push=%v", err)
	}
	select {
	case pending := <-full.frames:
		pending.complete(nil, context.Canceled, false)
	default:
	}
	if len(full.slots) != 0 {
		t.Fatal("canceled push leaked reservation")
	}
}
