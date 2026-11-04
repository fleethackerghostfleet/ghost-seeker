package ingest

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/link"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
)

type nopLink struct{ est *link.Estimator }

func (l nopLink) Exchange(context.Context, *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	return &pb.ExchangeResponse{}, nil
}
func (l nopLink) State() *pb.LinkState { return l.est.State() }

func newIngester(t *testing.T, cfg Config) (*Ingester, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := clock.NewVirtual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	sch, err := scheduler.New(scheduler.DefaultConfig("t"), st, nopLink{link.NewEstimator(64000, 300, 1e9)}, scoring.Default(), clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, inference.NewMock(), sch, clk, nil), st
}

// scene is a gradient with sensor noise: PNG compresses it poorly, like a
// real photo, and JPEG well.
func scene(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8(r.Intn(24))
			img.Set(x, y, color.RGBA{uint8(x*230/w) + n, uint8(y*230/h) + n, 120 + n, 255})
		}
	}
	return img
}

func encPNG(t *testing.T, img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(t *testing.T, img image.Image) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decode(t *testing.T, data []byte) (image.Image, string) {
	t.Helper()
	img, f, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img, f
}

func ingest(t *testing.T, g *Ingester, st *store.Store, data []byte, hints map[string]string) *store.Row {
	t.Helper()
	row, err := g.Ingest(context.Background(), &Capture{Type: pb.ItemType_ITEM_TYPE_IMAGE, Data: data, Source: "cam", Hints: hints})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func form(t *testing.T, st *store.Store, row *store.Row, k pb.FormKind) ([]byte, *store.FormState) {
	t.Helper()
	f := row.Form(k)
	if f == nil {
		t.Fatalf("no %v form", k)
	}
	data, err := st.ReadFormAll(row.Item.Id, k)
	if err != nil {
		t.Fatal(err)
	}
	return data, f
}

func TestImageFormsPNGSource(t *testing.T) {
	g, st := newIngester(t, DefaultConfig())
	src := encPNG(t, scene(1280, 720))
	// Mock analyzer reports one box at (0.3,0.3) size 0.4×0.4.
	row := ingest(t, g, st, src, map[string]string{"label": "cargo ship", "conf": "0.9"})

	th, thf := form(t, st, row, pb.FormKind_FORM_KIND_THUMB)
	img, f := decode(t, th)
	if f != "jpeg" || thf.Mime != "image/jpeg" {
		t.Fatalf("thumb format %s mime %s", f, thf.Mime)
	}
	if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 90 {
		t.Fatalf("thumb %v, want 160x90", b)
	}

	cr, crf := form(t, st, row, pb.FormKind_FORM_KIND_CROP)
	img, f = decode(t, cr)
	if f != "jpeg" || crf.Mime != "image/jpeg" {
		t.Fatalf("crop format %s mime %s", f, crf.Mime)
	}
	// 0.4 box + 15% padding per side = 0.52 of each dimension, native res.
	if b := img.Bounds(); b.Dx() < 660 || b.Dx() > 670 || b.Dy() < 370 || b.Dy() > 377 {
		t.Fatalf("crop %v, want ~666x374", b)
	}

	full, ff := form(t, st, row, pb.FormKind_FORM_KIND_FULL)
	img, f = decode(t, full)
	if f != "jpeg" || ff.Mime != "image/jpeg" {
		t.Fatalf("full format %s mime %s", f, ff.Mime)
	}
	if img.Bounds().Dx() != 1280 || len(full) >= len(src) {
		t.Fatalf("full %v %dB, source %dB", img.Bounds(), len(full), len(src))
	}
	if !(len(th) < len(cr) && len(cr) < len(full)) {
		t.Fatalf("ladder not ascending: thumb %d crop %d full %d", len(th), len(cr), len(full))
	}
}

func TestJPEGFullPassthroughAndNoCropWithoutBoxes(t *testing.T) {
	g, st := newIngester(t, DefaultConfig())
	src := encJPEG(t, scene(640, 480))
	row := ingest(t, g, st, src, map[string]string{"unknownness": "0.9"})
	if row.Form(pb.FormKind_FORM_KIND_CROP) != nil {
		t.Fatal("crop without detections")
	}
	full, ff := form(t, st, row, pb.FormKind_FORM_KIND_FULL)
	if !bytes.Equal(full, src) || ff.Mime != "image/jpeg" {
		t.Fatalf("jpeg FULL should pass through unchanged (mime %s)", ff.Mime)
	}
}

func TestCropDroppedWhenNotSmallerThanFull(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CropQual = 100
	g, st := newIngester(t, cfg)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, scene(640, 480), &jpeg.Options{Quality: 10}); err != nil {
		t.Fatal(err)
	}
	// A low-quality JPEG source: a q100 crop of it encodes larger than FULL.
	row := ingest(t, g, st, b.Bytes(), map[string]string{"label": "cargo ship", "conf": "0.9"})
	if f := row.Form(pb.FormKind_FORM_KIND_CROP); f != nil {
		t.Fatalf("crop %dB kept although FULL is %dB", f.Bytes, b.Len())
	}
}

func TestFullQualityZeroKeepsPNG(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FullQual = 0
	g, st := newIngester(t, cfg)
	src := encPNG(t, scene(320, 240))
	row := ingest(t, g, st, src, nil)
	full, ff := form(t, st, row, pb.FormKind_FORM_KIND_FULL)
	if !bytes.Equal(full, src) || ff.Mime != "image/png" {
		t.Fatalf("want original PNG bytes with image/png, got mime %s", ff.Mime)
	}
}

func TestThumbConfigurable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ThumbWidth, cfg.ThumbHeight, cfg.ThumbQual = 96, 96, 40
	g, st := newIngester(t, cfg)
	// Portrait: height is the binding constraint.
	row := ingest(t, g, st, encJPEG(t, scene(522, 928)), nil)
	th, _ := form(t, st, row, pb.FormKind_FORM_KIND_THUMB)
	img, _ := decode(t, th)
	if b := img.Bounds(); b.Dy() != 96 || b.Dx() != 54 {
		t.Fatalf("thumb %v, want 54x96", b)
	}
	// Never upscale.
	row = ingest(t, g, st, encJPEG(t, scene(64, 48)), nil)
	th, _ = form(t, st, row, pb.FormKind_FORM_KIND_THUMB)
	if img, _ = decode(t, th); img.Bounds().Dx() != 64 {
		t.Fatalf("thumb upscaled to %v", img.Bounds())
	}
}

func TestCropRect(t *testing.T) {
	b := image.Rect(0, 0, 1000, 500)
	box := func(x, y, w, h, c float32) *pb.Detection {
		return &pb.Detection{Label: "x", Confidence: c, Box: &pb.Box{X: x, Y: y, W: w, H: h}}
	}
	cases := []struct {
		name string
		dets []*pb.Detection
		want image.Rectangle
		ok   bool
	}{
		{"none", nil, image.Rectangle{}, false},
		{"no box", []*pb.Detection{{Label: "x", Confidence: 0.9}}, image.Rectangle{}, false},
		{"single padded", []*pb.Detection{box(0.4, 0.4, 0.2, 0.2, 0.9)}, image.Rect(370, 185, 630, 315), true},
		{"union of two", []*pb.Detection{box(0.1, 0.1, 0.1, 0.1, 0.9), box(0.5, 0.5, 0.1, 0.2, 0.9)},
			image.Rect(24, 5, 675, 395), true},
		{"clamped at edge", []*pb.Detection{box(0, 0, 0.2, 0.2, 0.9)}, image.Rect(0, 0, 230, 115), true},
		{"low conf ignored", []*pb.Detection{box(0.4, 0.4, 0.2, 0.2, 0.9), box(0, 0, 0.1, 0.1, 0.1)},
			image.Rect(370, 185, 630, 315), true},
	}
	for _, c := range cases {
		got, ok := cropRect(b, c.dets, 0.15, 0.3)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestCropSkippedWhenNearlyFullFrame(t *testing.T) {
	g, _ := newIngester(t, DefaultConfig())
	img := scene(400, 300)
	r, err := g.cropForm(img, []*pb.Detection{{Confidence: 0.9, Box: &pb.Box{X: 0.05, Y: 0.05, W: 0.9, H: 0.9}}})
	if err != nil || r != nil {
		t.Fatalf("crop over most of the frame should be skipped (err %v, %d bytes)", err, len(r))
	}
}
