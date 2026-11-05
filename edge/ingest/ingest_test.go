package ingest

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

type spaceClock struct {
	clock.Clock
	onSleep func()
}

func (c spaceClock) Sleep(ctx context.Context, d time.Duration) error {
	c.onSleep()
	return ctx.Err()
}

func TestSightingUpdateWaitsForSpace(t *testing.T) {
	an := &scripted{script: map[int][]*pb.Sighting{1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "boat")}, 2: {sighting("s1", pb.Sighting_EVENT_UPDATE, false, strings.Repeat("subtype", 100))}}}
	g, st, clk := newIngester(t, DedupSightings, an)
	row, err := g.Ingest(context.Background(), capture(clk, 40))
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, f := range row.Forms {
		bytes += f.Bytes
	}
	if err := st.SetLimits(context.Background(), store.Limits{MaxBytes: bytes}); err != nil {
		t.Fatal(err)
	}
	g.cfg.WaitForSpace = true
	sleeps := 0
	g.clk = spaceClock{Clock: clk, onSleep: func() {
		sleeps++
		if err := st.SetLimits(context.Background(), store.Limits{}); err != nil {
			t.Fatal(err)
		}
	}}
	updated, err := g.Ingest(context.Background(), capture(clk, 50))
	if err != nil {
		t.Fatal(err)
	}
	if sleeps != 1 || an.calls != 2 || updated.Item.Features.Tags["subtype"] != strings.Repeat("subtype", 100) {
		t.Fatalf("update lost or reanalyzed: sleeps=%d calls=%d row=%v", sleeps, an.calls, updated)
	}
	data, err := st.ReadFormAll(row.Item.Id, pb.FormKind_FORM_KIND_FACT)
	if err != nil || !bytesContains(data, strings.Repeat("subtype", 100)) {
		t.Fatalf("FACT not updated: %s %v", data, err)
	}
}

func bytesContains(data []byte, text string) bool { return strings.Contains(string(data), text) }

// scripted returns the sightings scripted for the n-th Analyze call (1-based)
// and records the frames it was handed.
type scripted struct {
	script map[int][]*pb.Sighting
	empty  map[int]bool // calls that see no detections at all (open ocean)
	calls  int
	frames []inference.Frame
}

func (f *scripted) Analyze(_ context.Context, fr inference.Frame, _ []string, _ map[string]string) (*pb.AnalyzeResponse, error) {
	f.calls++
	f.frames = append(f.frames, fr)
	resp := &pb.AnalyzeResponse{ModelId: "scripted", Embedding: []float32{1, 0}, Unknownness: 0.2, Sightings: f.script[f.calls]}
	if !f.empty[f.calls] {
		resp.Detections = []*pb.Detection{{Label: "boat", Confidence: 0.5, Box: &pb.Box{X: 0.1, Y: 0.1, W: 0.2, H: 0.2}}}
	}
	return resp, nil
}

func (f *scripted) EmbedText(context.Context, []string) ([][]float32, error) { return nil, nil }

type noLink struct{}

func (noLink) Exchange(context.Context, *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	return &pb.ExchangeResponse{}, nil
}
func (noLink) State() *pb.LinkState { return &pb.LinkState{} }

func newIngester(t *testing.T, mode DedupMode, an inference.Analyzer) (*Ingester, *store.Store, *clock.Virtual) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	sch, err := scheduler.New(scheduler.DefaultConfig("t"), st, noLink{}, scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.EdgeID, cfg.Dedup, cfg.SceneEvery = "t", mode, 0 // scene throttle is exercised by TestSceneThrottle
	return New(cfg, st, an, sch, clk, nil), st, clk
}

func frameJPEG(shade uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{shade, uint8(x), uint8(y), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func capture(clk *clock.Virtual, shade uint8) *Capture {
	clk.Advance(200 * time.Millisecond)
	return &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: frameJPEG(shade), CapturedAt: clk.Now(), Source: "cam-a"}
}

func sighting(id string, ev pb.Sighting_Event, key bool, subtype string) *pb.Sighting {
	return &pb.Sighting{Id: id, Event: ev, IsKeyFrame: key, Subtype: subtype, Value: 0.7, NFrames: 3, Embedding: []float32{0, 1}, Unknownness: 0.6,
		KeyDetection: &pb.Detection{Label: "boat", Confidence: 0.9, Box: &pb.Box{X: 0.45, Y: 0.45, W: 0.05, H: 0.05}}}
}

func items(t *testing.T, st *store.Store) []*store.Row {
	t.Helper()
	rows, err := st.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSightingsStateMachine(t *testing.T) {
	ctx := context.Background()
	an := &scripted{script: map[int][]*pb.Sighting{
		2: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "tug")},
		3: {sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "tug")},
		4: {sighting("s1", pb.Sighting_EVENT_UPDATE, true, "tug")},
		5: {sighting("s1", pb.Sighting_EVENT_UPDATE, false, "tanker")},
		6: {sighting("s1", pb.Sighting_EVENT_UPDATE, true, "tanker")},
		7: {sighting("s1", pb.Sighting_EVENT_CLOSE, false, "tanker")},
		8: {sighting("s2", pb.Sighting_EVENT_OPEN, true, ""), sighting("s3", pb.Sighting_EVENT_OPEN, true, "")},
	}}
	g, st, clk := newIngester(t, DedupSightings, an)

	// 1: stateless response -> per-frame item, as in hash mode.
	first, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil || first == nil || first.Form(pb.FormKind_FORM_KIND_CROP) != nil {
		t.Fatalf("frame 1: row=%v err=%v (no crop expected)", first, err)
	}
	if fr := an.frames[0]; fr.Source != "cam-a" || fr.CapturedAt.IsZero() {
		t.Fatalf("analyzer must see source + timestamp: %+v", fr)
	}

	// 2: OPEN -> item tagged with the sighting, CROP form cut from the key box.
	opened, err := g.Ingest(ctx, capture(clk, 2))
	if err != nil {
		t.Fatal(err)
	}
	tags := opened.Item.Features.Tags
	if tags["sighting_id"] != "s1" || tags["event"] != "open" || tags["subtype"] != "tug" {
		t.Fatalf("tags = %v", tags)
	}
	if d := opened.Item.Features.Detections; len(d) != 1 || d[0].Confidence != 0.9 {
		t.Fatalf("sighting item carries the key detection, got %v", d)
	}
	if f := opened.Item.Features; f.Embedding[1] != 1 || f.Unknownness != 0.6 {
		t.Fatalf("sighting item must carry the object's embedding/unknownness, got %v / %v", f.Embedding, f.Unknownness)
	}
	crop := opened.Form(pb.FormKind_FORM_KIND_CROP)
	if crop == nil {
		t.Fatal("no CROP form")
	}
	data, err := st.ReadFormAll(opened.Item.Id, pb.FormKind_FORM_KIND_CROP)
	if err != nil {
		t.Fatal(err)
	}
	cimg, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// 5% of 320x240 is 16x12 px; margin 1.5x is still tiny, so the floor of 96 px applies.
	if b := cimg.Bounds(); b.Dx() != 96 || b.Dy() != 96 {
		t.Fatalf("crop bounds %v, want 96x96", b)
	}
	fullBefore := opened.Form(pb.FormKind_FORM_KIND_FULL).Hash

	// 3: DUPLICATE -> no item, counted.
	if row, err := g.Ingest(ctx, capture(clk, 3)); err != nil || row != nil {
		t.Fatalf("duplicate: row=%v err=%v", row, err)
	}
	if n := len(items(t, st)); n != 2 || g.Deduped() != 1 {
		t.Fatalf("after duplicate: items=%d dups=%d", n, g.Deduped())
	}

	// 4: better key frame before anything shipped -> payloads replaced in place.
	rep, err := g.Ingest(ctx, capture(clk, 4))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Item.Id != opened.Item.Id || len(items(t, st)) != 2 || g.Replaced != 1 {
		t.Fatalf("expected in-place replacement of %s, got %s (items=%d replaced=%d)", opened.Item.Id, rep.Item.Id, len(items(t, st)), g.Replaced)
	}
	if f := rep.Form(pb.FormKind_FORM_KIND_FULL); f.Hash == fullBefore || f.AckedThrough != 0 || f.Complete {
		t.Fatalf("FULL not replaced/reset: %+v", f)
	}
	for _, pf := range rep.Item.Forms {
		if st := rep.Form(pf.Kind); st == nil || st.Hash != pf.ContentHash || st.Bytes != pf.Bytes {
			t.Fatalf("Item.Forms out of sync with stored forms after replacement: %v vs %+v", pf, st)
		}
	}
	if rep.Item.Features.Tags["event"] != "update" {
		t.Fatalf("tags = %v", rep.Item.Features.Tags)
	}

	// 5: non-key UPDATE (subtype flip) -> same item, tags refreshed, FACT re-queued.
	rt, err := g.Ingest(ctx, capture(clk, 5))
	if err != nil {
		t.Fatal(err)
	}
	if rt.Item.Id != opened.Item.Id || rt.Item.Features.Tags["subtype"] != "tanker" || len(items(t, st)) != 2 {
		t.Fatalf("retag: id=%s tags=%v items=%d", rt.Item.Id, rt.Item.Features.Tags, len(items(t, st)))
	}

	// 6: FULL started shipping -> a better key frame becomes a new item with the same sighting_id.
	if err := st.SetAcked(ctx, opened.Item.Id, pb.FormKind_FORM_KIND_FULL, 1000, false); err != nil {
		t.Fatal(err)
	}
	second, err := g.Ingest(ctx, capture(clk, 6))
	if err != nil {
		t.Fatal(err)
	}
	if second.Item.Id == opened.Item.Id || second.Item.Features.Tags["sighting_id"] != "s1" || len(items(t, st)) != 3 {
		t.Fatalf("expected a linked new item, got %s tags=%v items=%d", second.Item.Id, second.Item.Features.Tags, len(items(t, st)))
	}

	// 7: CLOSE -> tiny RECORD contact report pointing at the current item.
	rec, err := g.Ingest(ctx, capture(clk, 7))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Item.Type != pb.ItemType_ITEM_TYPE_RECORD || rec.Item.Features.Tags["event"] != "close" || rec.Item.Features.Tags["sighting_id"] != "s1" {
		t.Fatalf("close record: %v", rec.Item)
	}
	if f := rec.Form(pb.FormKind_FORM_KIND_FACT); f == nil || f.Bytes > 400 || !bytes.Contains(mustRead(t, st, rec.Item.Id, pb.FormKind_FORM_KIND_FACT), []byte(second.Item.Id)) {
		t.Fatalf("contact report should be a small FACT naming the item: %+v", f)
	}
	if _, open := g.sightings["s1"]; open {
		t.Fatal("state not dropped on close")
	}

	// 8: two objects in one frame -> two items from the same bytes (hash dedup must not fold them).
	if _, err := g.Ingest(ctx, capture(clk, 8)); err != nil {
		t.Fatal(err)
	}
	if n := len(items(t, st)); n != 6 {
		t.Fatalf("items after two OPENs = %d, want 6", n)
	}
	if g.Count != 6 {
		t.Fatalf("Count = %d", g.Count)
	}
}

func TestSightingErrorsPropagate(t *testing.T) {
	ctx := context.Background()
	an := &scripted{script: map[int][]*pb.Sighting{
		1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, ""), sighting("s2", pb.Sighting_EVENT_OPEN, true, "")},
		2: {sighting("s3", pb.Sighting_EVENT_OPEN, true, "")},
	}}
	g, st, clk := newIngester(t, DedupSightings, an)
	if _, err := g.Ingest(ctx, capture(clk, 1)); err != nil {
		t.Fatal(err)
	}
	if n := len(items(t, st)); n != 2 {
		t.Fatalf("items = %d, want 2", n)
	}
	st.Close() // every store write now fails: the error must surface, not just be logged
	if _, err := g.Ingest(ctx, capture(clk, 2)); err == nil {
		t.Fatal("expected an error from a failed OPEN")
	}
}

func TestSceneThrottle(t *testing.T) {
	ctx := context.Background()
	an := &scripted{script: map[int][]*pb.Sighting{6: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "")}},
		empty: map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 8: true, 9: true}}
	g, st, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Second
	// 1: first object-free frame -> scene item: FACT+THUMB on the ladder, FULL kept for pulls
	scene, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil || scene == nil {
		t.Fatalf("scene: %v %v", scene, err)
	}
	if scene.Item.Features.Tags["event"] != "scene" || scene.Form(pb.FormKind_FORM_KIND_FULL) == nil || scene.Form(pb.FormKind_FORM_KIND_THUMB) == nil || scene.Form(pb.FormKind_FORM_KIND_FACT) == nil {
		t.Fatalf("scene item: tags=%v forms=%+v", scene.Item.Features.Tags, scene.Forms)
	}
	// 2-5: within SceneEvery (200 ms apart) -> folded
	for i := 2; i <= 5; i++ {
		if row, err := g.Ingest(ctx, capture(clk, uint8(i))); err != nil || row != nil {
			t.Fatalf("frame %d: row=%v err=%v", i, row, err)
		}
	}
	// 6: OPEN -> normal sighting item with FULL; the engine is now known to be stateful for cam-a
	clk.Advance(time.Second)
	opened, err := g.Ingest(ctx, capture(clk, 6))
	if err != nil || opened == nil || opened.Form(pb.FormKind_FORM_KIND_FULL) == nil {
		t.Fatalf("open: %v %v", opened, err)
	}
	// 7: detections but no sighting (tracker warming up on a new object) -> folded
	if row, err := g.Ingest(ctx, capture(clk, 7)); err != nil || row != nil {
		t.Fatalf("warm-up frame: row=%v err=%v", row, err)
	}
	// 8: object-free again, window elapsed since scene #1 -> second scene item; 9: folded
	clk.Advance(time.Second)
	if row, err := g.Ingest(ctx, capture(clk, 8)); err != nil || row == nil || row.Item.Features.Tags["event"] != "scene" {
		t.Fatalf("second scene: %v %v", row, err)
	}
	if row, err := g.Ingest(ctx, capture(clk, 9)); err != nil || row != nil {
		t.Fatalf("frame 9: row=%v err=%v", row, err)
	}
	if n := len(items(t, st)); n != 3 || g.Deduped() != 6 {
		t.Fatalf("items=%d dups=%d, want 3/6", n, g.Deduped())
	}
	// a second source has its own window
	an.empty[10] = true
	if row, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: frameJPEG(10), CapturedAt: clk.Now(), Source: "cam-b"}); err != nil || row == nil {
		t.Fatalf("other source: %v %v", row, err)
	}
}

func TestSceneThrottleStatelessAnalyzer(t *testing.T) {
	// A stateless engine (inference.Mock, an older sidecar) never reports sightings: detected frames must still
	// be stored per frame, only object-free frames are throttled.
	ctx := context.Background()
	an := &scripted{empty: map[int]bool{2: true, 3: true}}
	g, st, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Minute
	for i := 1; i <= 4; i++ {
		if _, err := g.Ingest(ctx, capture(clk, uint8(i))); err != nil {
			t.Fatal(err)
		}
	}
	rows := items(t, st)
	if len(rows) != 3 || g.Deduped() != 1 { // frames 1 and 4 (detections) + one scene for 2/3
		t.Fatalf("items=%d dups=%d, want 3/1", len(rows), g.Deduped())
	}
	full := 0
	for _, r := range rows {
		if r.Item.Features.Tags["event"] != "scene" && r.Form(pb.FormKind_FORM_KIND_FULL) != nil {
			full++
		}
	}
	if full != 2 {
		t.Fatalf("detected frames must keep FULL as normal items: %d", full)
	}
}

func TestSceneDoesNotSwallowDetectedTwin(t *testing.T) {
	// Same bytes: first analysed as empty (-> scene), then with a detection (stateless engine).
	// The detected frame must become a full item instead of folding into the scene.
	ctx := context.Background()
	an := &scripted{empty: map[int]bool{1: true}}
	g, st, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Minute
	data := frameJPEG(3)
	if _, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, CapturedAt: clk.Now(), Source: "cam-a"}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	row, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, CapturedAt: clk.Now(), Source: "cam-a"})
	if err != nil || row == nil || row.Form(pb.FormKind_FORM_KIND_FULL) == nil || row.Item.Features.Tags["event"] == "scene" {
		t.Fatalf("detected twin: %v %v", row, err)
	}
	if n := len(items(t, st)); n != 2 || g.Deduped() != 0 {
		t.Fatalf("items=%d dups=%d, want 2/0", n, g.Deduped())
	}
	// and a true duplicate of a full item still folds
	if row2, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, CapturedAt: clk.Now(), Source: "cam-a"}); err != nil || row2 == nil || row2.Item.Id != row.Item.Id || g.Deduped() != 1 {
		t.Fatalf("hash fold: %v %v dups=%d", row2, err, g.Deduped())
	}
}

func TestSceneWriteFailureDoesNotAdvanceWindow(t *testing.T) {
	ctx := context.Background()
	an := &scripted{empty: map[int]bool{1: true}}
	g, st, clk := newIngester(t, DedupSightings, an)
	g.cfg.SceneEvery = time.Minute
	st.Close()
	if _, err := g.Ingest(ctx, capture(clk, 1)); err == nil {
		t.Fatal("expected store error")
	}
	if _, ok := g.lastScene["cam-a"]; ok {
		t.Fatal("failed scene must not start the throttle window")
	}
}

func TestSightingsFallsBackToHash(t *testing.T) {
	ctx := context.Background()
	g, st, clk := newIngester(t, DedupSightings, &scripted{})
	a, _ := g.Ingest(ctx, capture(clk, 9))
	b, _ := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: frameJPEG(9), CapturedAt: clk.Now(), Source: "cam-a"})
	if a.Item.Id != b.Item.Id || g.Deduped() != 1 || len(items(t, st)) != 1 {
		t.Fatalf("identical frames without sightings must fold: %s %s dups=%d", a.Item.Id, b.Item.Id, g.Deduped())
	}
}

func TestHashAndNaiveModes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		mode  DedupMode
		items int
		dups  int64
	}{{DedupHash, 1, 1}, {DedupNaive, 2, 0}} {
		an := &scripted{script: map[int][]*pb.Sighting{1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "")}}}
		g, st, clk := newIngester(t, tc.mode, an)
		data := frameJPEG(5)
		row, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, CapturedAt: clk.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if row.Form(pb.FormKind_FORM_KIND_CROP) != nil || row.Item.Features.Tags["sighting_id"] != "" {
			t.Fatalf("%v: sightings must be ignored outside sightings mode", tc.mode)
		}
		clk.Advance(time.Second)
		if _, err := g.Ingest(ctx, &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, CapturedAt: clk.Now()}); err != nil {
			t.Fatal(err)
		}
		if n := len(items(t, st)); n != tc.items || g.Deduped() != tc.dups {
			t.Fatalf("%v: items=%d dups=%d, want %d/%d", tc.mode, n, g.Deduped(), tc.items, tc.dups)
		}
	}
}

func TestParseDedup(t *testing.T) {
	for in, want := range map[string]DedupMode{"": DedupHash, "hash": DedupHash, "NAIVE": DedupNaive, "sightings": DedupSightings} {
		if got, err := ParseDedup(in); err != nil || got != want {
			t.Fatalf("ParseDedup(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseDedup("fuzzy"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCropGeometry(t *testing.T) {
	g := &Ingester{cfg: DefaultConfig()}
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for _, tc := range []struct {
		box  *pb.Box
		w, h int
	}{
		{&pb.Box{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}, 480, 360}, // 320x240 box * 1.5
		{&pb.Box{X: 0.0, Y: 0.0, W: 0.1, H: 0.1}, 96, 96},     // corner: floor applies, window slides inside the frame
		{&pb.Box{X: 0.9, Y: 0.9, W: 0.1, H: 0.1}, 96, 96},     // far corner likewise
		{&pb.Box{X: 0.0, Y: 0.0, W: 1, H: 1}, 640, 480},       // whole frame: never larger than the frame
	} {
		data, err := g.crop(img, tc.box)
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if b := c.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
			t.Errorf("box %v: crop %dx%d, want %dx%d", tc.box, b.Dx(), b.Dy(), tc.w, tc.h)
		}
	}
}

func mustRead(t *testing.T, st *store.Store, id string, k pb.FormKind) []byte {
	t.Helper()
	b, err := st.ReadFormAll(id, k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
