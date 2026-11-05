package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	script     map[int][]*pb.Sighting
	empty      map[int]bool // calls that see no detections at all (open ocean)
	detections map[int][]*pb.Detection
	burst      *pb.NightBurst
	calls      int
	frames     []inference.Frame
}

func (f *scripted) Analyze(_ context.Context, fr inference.Frame, _ []string, _ map[string]string) (*pb.AnalyzeResponse, error) {
	f.calls++
	f.frames = append(f.frames, fr)
	resp := &pb.AnalyzeResponse{ModelId: "scripted", Embedding: []float32{1, 0}, Unknownness: 0.2,
		Sightings: f.script[f.calls], Burst: f.burst}
	if detections, ok := f.detections[f.calls]; ok {
		resp.Detections = detections
	} else if !f.empty[f.calls] {
		resp.Detections = []*pb.Detection{{Label: "boat", Confidence: 0.5, Box: &pb.Box{X: 0.1, Y: 0.1, W: 0.2, H: 0.2}}}
	}
	return resp, nil
}

func TestIngestCallsOnBurst(t *testing.T) {
	an := &scripted{burst: &pb.NightBurst{Fps: 8, DurationS: 2.5, Reason: "flashing light"}}
	g, _, clk := newIngester(t, DedupNaive, an)
	var gotSource, gotReason string
	var gotFPS float64
	var gotDuration time.Duration
	g.OnBurst = func(source string, fps float64, d time.Duration, reason string) {
		gotSource, gotFPS, gotDuration, gotReason = source, fps, d, reason
	}
	if _, err := g.Ingest(context.Background(), capture(clk, 4)); err != nil {
		t.Fatal(err)
	}
	if gotSource != "cam-a" || gotFPS != 8 || gotDuration != 2500*time.Millisecond || gotReason != "flashing light" {
		t.Fatalf("OnBurst = %q, %g, %v, %q", gotSource, gotFPS, gotDuration, gotReason)
	}
}

func TestDarkContactSightingsFeatureAndTag(t *testing.T) {
	g, _, _ := newIngester(t, DedupSightings, &scripted{})
	box := &pb.Box{X: 0.4, Y: 0.4, W: 0.1, H: 0.1}
	resp := &pb.AnalyzeResponse{Detections: []*pb.Detection{
		{Label: "boat", Confidence: 0.8, Box: box},
		{Label: "dark contact", Confidence: 0.9, Box: &pb.Box{X: 0.41, Y: 0.41, W: 0.1, H: 0.1}},
	}}
	item := &pb.Item{Features: &pb.Features{}}
	g.applyFeatures(item, resp, &pb.Sighting{Id: "s1", Event: pb.Sighting_EVENT_OPEN,
		KeyDetection: &pb.Detection{Label: "boat", Confidence: 0.8, Box: box}})
	if len(item.Features.Detections) != 2 || item.Features.Detections[1].Label != "dark contact" ||
		item.Features.Tags["dark_contact"] != "1" {
		t.Fatalf("sighting dark-contact features = %+v", item.Features)
	}

	item = &pb.Item{Features: &pb.Features{}}
	g.applyFeatures(item, resp, nil)
	if item.Features.Tags["dark_contact"] != "1" {
		t.Fatalf("stateless dark-contact tag = %v", item.Features.Tags)
	}

	resp.Detections[1].Box = &pb.Box{X: 0.8, Y: 0.8, W: 0.1, H: 0.1}
	item = &pb.Item{Features: &pb.Features{}}
	g.applyFeatures(item, resp, &pb.Sighting{Id: "s2", Event: pb.Sighting_EVENT_OPEN,
		KeyDetection: &pb.Detection{Label: "boat", Confidence: 0.8, Box: box}})
	if len(item.Features.Detections) != 1 || item.Features.Tags["dark_contact"] != "" {
		t.Fatalf("unmatched dark contact attached to sighting: %+v", item.Features)
	}
}

func TestDuplicateDarkContactUpdatesKnownSighting(t *testing.T) {
	ctx := context.Background()
	box := &pb.Box{X: 0.45, Y: 0.45, W: 0.05, H: 0.05}
	an := &scripted{
		script: map[int][]*pb.Sighting{
			1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "tug")},
			2: {sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "tug")},
		},
		detections: map[int][]*pb.Detection{
			2: {{Label: "dark contact", Confidence: 0.9, Box: box}},
		},
	}
	g, st, clk := newIngester(t, DedupSightings, an)
	opened, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil {
		t.Fatal(err)
	}
	fullHash := opened.Form(pb.FormKind_FORM_KIND_FULL).Hash

	duplicate, err := g.Ingest(ctx, capture(clk, 2))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate == nil || duplicate.Item.Id != opened.Item.Id || g.Deduped() != 1 {
		t.Fatalf("duplicate row = %v, dups = %d", duplicate, g.Deduped())
	}
	stored, err := st.Get(ctx, opened.Item.Id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Form(pb.FormKind_FORM_KIND_FULL).Hash != fullHash || g.Replaced != 0 {
		t.Fatalf("duplicate replaced key frame/form: form=%+v replaced=%d", duplicate.Form(pb.FormKind_FORM_KIND_FULL), g.Replaced)
	}
	if stored.Item.Features.Tags["dark_contact"] != "1" || len(stored.Item.Features.Detections) != 2 ||
		stored.Item.Features.Detections[0].Label != "boat" || stored.Item.Features.Detections[1].Label != "dark contact" {
		t.Fatalf("duplicate dark-contact features = %+v", stored.Item.Features)
	}
	foundDark := false
	for _, reason := range stored.Score.GetReasons() {
		if reason.GetScorer() == "dark" && reason.GetNote() == "dark contact: no navigation lights at night" {
			foundDark = true
			break
		}
	}
	if !foundDark {
		t.Fatalf("dark-contact reason missing after duplicate: %+v", stored.Score.GetReasons())
	}
}

func TestDuplicateDarkContactReplacesWithStrongerEvidence(t *testing.T) {
	ctx := context.Background()
	box := &pb.Box{X: 0.45, Y: 0.45, W: 0.05, H: 0.05}
	an := &scripted{
		script: map[int][]*pb.Sighting{
			1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "tug")},
			2: {sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "tug")},
			3: {sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "tug")},
		},
		detections: map[int][]*pb.Detection{
			2: {{Label: "dark contact", Confidence: 0.2, Box: box}},
			3: {{Label: "dark contact", Confidence: 0.9, Box: box}},
		},
	}
	g, st, clk := newIngester(t, DedupSightings, an)
	opened, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil {
		t.Fatal(err)
	}

	first, err := g.Ingest(ctx, capture(clk, 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range first.Score.GetReasons() {
		if reason.GetScorer() == "dark" {
			t.Fatalf("below-threshold duplicate produced dark reason: %+v", reason)
		}
	}

	if _, err := g.Ingest(ctx, capture(clk, 3)); err != nil {
		t.Fatal(err)
	}
	stored, err := st.Get(ctx, opened.Item.Id)
	if err != nil {
		t.Fatal(err)
	}
	darkCount := 0
	for _, detection := range stored.Item.Features.Detections {
		if detection.GetLabel() == "dark contact" {
			darkCount++
			if detection.GetConfidence() != 0.9 {
				t.Fatalf("stored dark-contact confidence = %v, want 0.9", detection.GetConfidence())
			}
		}
	}
	if darkCount != 1 {
		t.Fatalf("stored %d dark-contact detections, want one: %+v", darkCount, stored.Item.Features.Detections)
	}
	foundDark := false
	for _, reason := range stored.Score.GetReasons() {
		if reason.GetScorer() == "dark" && reason.GetNote() == "dark contact: no navigation lights at night" {
			foundDark = true
			break
		}
	}
	if !foundDark {
		t.Fatalf("stronger duplicate dark-contact reason missing: %+v", stored.Score.GetReasons())
	}
}

func TestReplaceKeyFrameClearsDarkContactTag(t *testing.T) {
	ctx := context.Background()
	box := &pb.Box{X: 0.45, Y: 0.45, W: 0.05, H: 0.05}
	an := &scripted{
		script: map[int][]*pb.Sighting{
			1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "tug")},
			2: {sighting("s1", pb.Sighting_EVENT_UPDATE, true, "tug")},
		},
		detections: map[int][]*pb.Detection{
			1: {{Label: "dark contact", Confidence: 0.9, Box: box}},
			2: {{Label: "boat", Confidence: 0.9, Box: box}},
		},
	}
	g, st, clk := newIngester(t, DedupSightings, an)
	opened, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil {
		t.Fatal(err)
	}
	if opened.Item.Features.Tags["dark_contact"] != "1" {
		t.Fatalf("opened item not tagged dark: %v", opened.Item.Features.Tags)
	}
	replaced, err := g.Ingest(ctx, capture(clk, 2))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.Get(ctx, opened.Item.Id)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Item.Id != opened.Item.Id || g.Replaced != 1 {
		t.Fatalf("lit key frame did not replace the same item: id=%s replaced=%d", replaced.Item.Id, g.Replaced)
	}
	if _, tagged := stored.Item.Features.Tags["dark_contact"]; tagged {
		t.Fatalf("lit replacement retained dark-contact tag: %v", stored.Item.Features.Tags)
	}
	for _, detection := range stored.Item.Features.Detections {
		if detection.GetLabel() == "dark contact" {
			t.Fatalf("lit replacement retained dark-contact detection: %+v", stored.Item.Features.Detections)
		}
	}
}

func TestReplayBurstShortensAndDecays(t *testing.T) {
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	replay := &Replay{clk: clk, period: 10 * time.Second,
		queue: []*Capture{{Source: "cam-a"}, {Source: "cam-a"}, {Source: "cam-a"}, {Source: "cam-a"}, {Source: "cam-a"}}}
	if _, _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	replay.Burst("cam-a", 5, 500*time.Millisecond)
	for i := 0; i < 3; i++ {
		if _, _, err := replay.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := clk.Now().Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); elapsed != 600*time.Millisecond {
		t.Fatalf("burst elapsed = %v, want 600ms", elapsed)
	}
	if _, _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := clk.Now().Sub(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); elapsed != 10*time.Second+600*time.Millisecond {
		t.Fatalf("base-rate elapsed after burst = %v", elapsed)
	}
}

func TestReplayBurstDoesNotShortenOtherSource(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewVirtual(start)
	replay := &Replay{clk: clk, period: 10 * time.Second,
		queue: []*Capture{{Source: "cam-a"}, {Source: "cam-b"}, {Source: "cam-a"}}}
	if _, _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	replay.Burst("cam-a", 5, 30*time.Second)
	if _, _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := clk.Now().Sub(start); elapsed != 10*time.Second {
		t.Fatalf("source B wait = %v, want base period 10s", elapsed)
	}
	if _, _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := clk.Now().Sub(start); elapsed != 10*time.Second+200*time.Millisecond {
		t.Fatalf("source A wait after B = %v, want 10.2s", elapsed)
	}
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
	if err != nil || first == nil || first.Item.Features.Tags["sighting_id"] != "" {
		t.Fatalf("frame 1: row=%v err=%v (no sighting expected)", first, err)
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
		// Per-frame detections, not the sighting's key detection (conf 0.9).
		if d := row.Item.Features.Detections; len(d) != 1 || d[0].Confidence == 0.9 || row.Item.Features.Tags["sighting_id"] != "" {
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

// unknownOn makes the scripted analyzer report the n-th frame as unknown.
type unknownOn struct {
	*scripted
	n int
}

func (u unknownOn) Analyze(ctx context.Context, fr inference.Frame, l []string, h map[string]string) (*pb.AnalyzeResponse, error) {
	r, err := u.scripted.Analyze(ctx, fr, l, h)
	if u.calls == u.n {
		r.Unknownness = 0.9
	}
	return r, err
}

func TestReplacedKeyFrameRecutsExistingCrop(t *testing.T) {
	ctx := context.Background()
	an := unknownOn{&scripted{script: map[int][]*pb.Sighting{
		1: {sighting("s1", pb.Sighting_EVENT_OPEN, true, "")},
		2: {sighting("s1", pb.Sighting_EVENT_UPDATE, true, "")},
	}}, 2}
	g, _, clk := newIngester(t, DedupSightings, an)
	opened, err := g.Ingest(ctx, capture(clk, 1))
	if err != nil || opened.Form(pb.FormKind_FORM_KIND_CROP) == nil {
		t.Fatalf("open: %v %v", opened, err)
	}
	before := opened.Form(pb.FormKind_FORM_KIND_CROP).Hash
	// The replacement frame is unknown, so imageForms alone would skip CROP.
	rep, err := g.Ingest(ctx, capture(clk, 200))
	if err != nil || rep.Item.Id != opened.Item.Id {
		t.Fatalf("replace: %v %v", rep, err)
	}
	f := rep.Form(pb.FormKind_FORM_KIND_CROP)
	if f == nil || f.Hash == before || f.Mime != "image/jpeg" {
		t.Fatalf("CROP must be re-cut from the new key frame: %+v (old hash %s)", f, before)
	}
}

// A sighting the engine opens late (tentative single detection reported
// tentative_after_s after it was seen) is cut from the buffered frame nearest
// its last_seen, not from the object-free frame in hand.
func TestLateOpenUsesBufferedFrame(t *testing.T) {
	ctx := context.Background()
	unknown := sighting("s1", pb.Sighting_EVENT_DUPLICATE, false, "") // not yet known: stores nothing, frames still buffered
	late := sighting("s1", pb.Sighting_EVENT_OPEN, false, "tentative")
	an := &scripted{script: map[int][]*pb.Sighting{1: {unknown}, 2: {unknown}, 3: {unknown}, 4: {late}}}
	g, st, clk := newIngester(t, DedupSightings, an)

	var caps []*Capture
	for shade := uint8(10); shade <= 30; shade += 10 {
		c := capture(clk, shade)
		caps = append(caps, c)
		if _, err := g.Ingest(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	seen := caps[1] // the object was in frame 2, 400 ms before the frame that carries the OPEN
	late.LastSeenUnixMs = seen.CapturedAt.UnixMilli() + 30
	now := capture(clk, 40)
	if _, err := g.Ingest(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows := items(t, st)
	if len(rows) != 1 {
		t.Fatalf("items = %d, want 1", len(rows))
	}
	sum := sha256.Sum256(seen.Data)
	if got := rows[0].Item.ContentHash; got != hex.EncodeToString(sum[:]) {
		t.Fatalf("item cut from the wrong frame: hash %s", got[:8])
	}
	if rows[0].Item.CapturedAtUnixMs != seen.CapturedAt.UnixMilli() {
		t.Fatalf("captured_at = %d, want %d", rows[0].Item.CapturedAtUnixMs, seen.CapturedAt.UnixMilli())
	}
	if g.LateKeyFrames != 1 {
		t.Fatalf("LateKeyFrames = %d", g.LateKeyFrames)
	}
	if full := mustRead(t, st, rows[0].Item.Id, pb.FormKind_FORM_KIND_FULL); len(full) == 0 {
		t.Fatal("no FULL form")
	}
}

// An OPEN whose last_seen is the frame in hand (the normal case) and one older
// than the buffer are both cut from the current frame.
func TestOpenInHandOrBeyondBufferUsesCurrentFrame(t *testing.T) {
	ctx := context.Background()
	open1 := sighting("s1", pb.Sighting_EVENT_OPEN, true, "")
	open2 := sighting("s2", pb.Sighting_EVENT_OPEN, false, "tentative")
	an := &scripted{script: map[int][]*pb.Sighting{1: {open1}, 2: {open2}}}
	g, st, clk := newIngester(t, DedupSightings, an)
	g.cfg.FrameBufferAge = 300 * time.Millisecond

	c1 := capture(clk, 10)
	open1.LastSeenUnixMs = c1.CapturedAt.UnixMilli()
	if _, err := g.Ingest(ctx, c1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Second)
	c2 := capture(clk, 20)
	open2.LastSeenUnixMs = c1.CapturedAt.UnixMilli() // older than the frame in hand, but c1 has aged out of the ring
	if _, err := g.Ingest(ctx, c2); err != nil {
		t.Fatal(err)
	}
	rows := items(t, st)
	if len(rows) != 2 {
		t.Fatalf("items = %d, want 2", len(rows))
	}
	for _, c := range []*Capture{c1, c2} {
		sum := sha256.Sum256(c.Data)
		found := false
		for _, r := range rows {
			found = found || r.Item.ContentHash == hex.EncodeToString(sum[:])
		}
		if !found {
			t.Fatalf("no item cut from frame at %d", c.CapturedAt.UnixMilli())
		}
	}
	if g.LateKeyFrames != 0 {
		t.Fatalf("LateKeyFrames = %d, want 0", g.LateKeyFrames)
	}
}

func TestRememberBounds(t *testing.T) {
	g, _, clk := newIngester(t, DedupSightings, &scripted{})
	g.cfg.FrameBufferAge, g.cfg.FrameBufferBytes = time.Second, 3*len(frameJPEG(1))+1
	for i := 0; i < 10; i++ {
		c := capture(clk, uint8(i))
		g.remember(c, "h")
	}
	ring := g.recent["cam-a"]
	if len(ring) != 3 {
		t.Fatalf("ring = %d frames, want 3 (bytes bound)", len(ring))
	}
	clk.Advance(5 * time.Second)
	g.remember(capture(clk, 99), "h2")
	if n := len(g.recent["cam-a"]); n != 1 {
		t.Fatalf("ring after 5 s gap = %d, want 1 (age bound)", n)
	}
}
