// Package ingest turns captures into stored, scored items with their
// fidelity-ladder forms. Sources are pluggable (frame directories/zips for
// replay, a camera or message bus in production).
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"maps"
	"math"
	"math/rand"
	"slices"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/image/draw"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
)

// Capture is one thing a source produced.
type Capture struct {
	Type       pb.ItemType
	Data       []byte // image bytes, record JSON, telemetry blob
	CapturedAt time.Time
	Source     string
	Geo        *pb.Geo
	Hints      map[string]string // ground truth for the mock analyzer; ignored by real ones
	Tags       map[string]string
}

type Source interface {
	// Next blocks (in source time) until the next capture; returns
	// io.EOF-like done=true when exhausted.
	Next(ctx context.Context) (c *Capture, done bool, err error)
}

type Config struct {
	EdgeID string // seeds id entropy; must differ per edge
	// THUMB: JPEG fitted within ThumbWidth × ThumbHeight px (0 = unbounded).
	ThumbWidth  int
	ThumbHeight int
	ThumbQual   int
	// CROP is a JPEG at CropQual, cut at native resolution. For sighting
	// items: key_detection.box scaled by CropMargin about its centre, at
	// least CropMinShort px on the short side. Otherwise: the union of
	// detection boxes (confidence >= CropMinConf) padded by CropPad × box
	// size per side, skipped when there are no boxes or the crop exceeds
	// CropMaxArea of the frame. A CROP no smaller than FULL is dropped.
	// Unknowns (unknownness >= the policy's UnknownMin) get no CROP unless
	// CropUnknowns is set: their whole frame is kept for training, so the
	// ladder goes THUMB → FULL.
	CropQual     int
	CropMargin   float64
	CropMinShort int
	CropPad      float64
	CropMinConf  float64
	CropMaxArea  float64
	CropUnknowns bool
	// FULL: JPEG/WebP captures are sent as-is; other formats are re-encoded
	// as JPEG at FullQual if smaller (<= 0 keeps the original bytes).
	FullQual    int
	KnownLabels []string
	// Dedup selects how captures become items; see DedupMode.
	Dedup DedupMode
	// SceneEvery (sightings mode) stores at most one object-free "scene"
	// frame per source per interval, FACT+THUMB only; 0 keeps hash behaviour
	// for object-free frames (one item per frame).
	SceneEvery time.Duration
}

// unknown reports whether an analyzer unknownness score marks the item
// unknown under the scheduler's current policy threshold.
func (g *Ingester) unknown(u float32) bool {
	min := float32(scoring.DefaultUnknownMin)
	if t := g.sch.Policy().GetThresholds(); t.GetUnknownMin() > 0 {
		min = t.GetUnknownMin()
	}
	return u >= min
}

func DefaultConfig() Config {
	return Config{ThumbWidth: 160, ThumbHeight: 160, ThumbQual: 55,
		CropQual: 80, CropMargin: 1.5, CropMinShort: 96, CropPad: 0.15, CropMinConf: 0, CropMaxArea: 0.6,
		FullQual: 90, Dedup: DedupHash, SceneEvery: 60 * time.Second,
		KnownLabels: []string{"cargo ship", "container ship", "sailboat", "fishing vessel", "tanker", "small boat", "buoy"}}
}

type Ingester struct {
	cfg Config
	st  *store.Store
	an  inference.Analyzer
	sch *scheduler.Scheduler
	clk clock.Clock
	log *slog.Logger
	ent *ulid.MonotonicEntropy
	// OnItem is called after each item is stored and scored.
	OnItem func(*store.Row)
	Count  int64
	Dups   int64 // captures folded into an existing item (exact hash or sighting DUPLICATE); read via Deduped
	// Replaced counts in-place key-frame replacements (sightings mode).
	Replaced int64
	// sightings maps an open sighting id to the item currently carrying its
	// key frame. In-memory only: after a restart a later key frame opens a
	// new item linked by tags["sighting_id"].
	sightings map[string]string
	lastScene map[string]time.Time // source -> captured-at of the last stored scene item
	stateful  map[string]bool      // source -> the engine has reported sightings for it
}

func New(cfg Config, st *store.Store, an inference.Analyzer, sch *scheduler.Scheduler, clk clock.Clock, log *slog.Logger) *Ingester {
	if log == nil {
		log = slog.Default()
	}
	return &Ingester{cfg: cfg, st: st, an: an, sch: sch, clk: clk, log: log, ent: ulid.Monotonic(entropyFor(cfg.EdgeID), 0),
		sightings: map[string]string{}, lastScene: map[string]time.Time{}, stateful: map[string]bool{}}
}

// Deduped is safe to read from other goroutines (console GetStatus).
func (g *Ingester) Deduped() int64 { return atomic.LoadInt64(&g.Dups) }

// Run drains a source until done or ctx is cancelled.
func (g *Ingester) Run(ctx context.Context, src Source) error {
	for {
		c, done, err := src.Next(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if _, err := g.Ingest(ctx, c); err != nil {
			g.log.Warn("ingest", "err", err)
		}
	}
}

// Ingest stores one capture. In hash mode (default) an exact-hash duplicate
// folds into the existing item; in sightings mode the engine decides which
// frames are new objects, better key frames, or duplicates (see sightings.go),
// and a response without sightings behaves like hash mode.
func (g *Ingester) Ingest(ctx context.Context, c *Capture) (*store.Row, error) {
	sum := sha256.Sum256(c.Data)
	hash := hex.EncodeToString(sum[:])
	image := c.Type == pb.ItemType_ITEM_TYPE_IMAGE
	if g.cfg.Dedup == DedupHash || (g.cfg.Dedup == DedupSightings && !image) {
		if row, err := g.existing(ctx, hash); row != nil || err != nil {
			return row, err
		}
	}
	var resp *pb.AnalyzeResponse
	if image {
		var err error
		resp, err = g.an.Analyze(ctx, inference.Frame{Image: c.Data, Source: c.Source, CapturedAt: c.CapturedAt}, g.cfg.KnownLabels, c.Hints)
		if err != nil {
			return nil, fmt.Errorf("analyze: %w", err)
		}
		if g.cfg.Dedup == DedupSightings {
			if len(resp.Sightings) > 0 {
				return g.applySightings(ctx, c, hash, resp)
			}
			if g.cfg.SceneEvery > 0 {
				return g.sceneItem(ctx, c, hash, resp)
			}
			if row, err := g.existing(ctx, hash); row != nil || err != nil {
				return row, err
			}
		}
	}
	return g.newItem(ctx, c, hash, resp, nil)
}

// existing returns the item already holding this exact content, if any.
func (g *Ingester) existing(ctx context.Context, hash string) (*store.Row, error) {
	id, ok, err := g.st.HasHash(ctx, hash)
	if err != nil || !ok {
		return nil, err
	}
	atomic.AddInt64(&g.Dups, 1)
	return g.st.Get(ctx, id)
}

// newItem builds, stores and scores one item. s, when set, is the sighting
// this image item reports: it tags the item and shapes its CROP form. only,
// when given, restricts the stored forms to those kinds.
func (g *Ingester) newItem(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting, only ...pb.FormKind) (*store.Row, error) {
	ts := c.CapturedAt
	if ts.IsZero() {
		ts = g.clk.Now()
	}
	it := &pb.Item{Id: ulid.MustNew(ulid.Timestamp(ts), g.ent).String(), Type: c.Type, CapturedAtUnixMs: ts.UnixMilli(),
		Source: c.Source, Geo: c.Geo, ContentHash: hash, Features: &pb.Features{Tags: c.Tags}}
	payloads := map[pb.FormKind][]byte{}
	var original []byte // kept when FULL is re-encoded, for an operator's original pull
	switch c.Type {
	case pb.ItemType_ITEM_TYPE_IMAGE:
		g.applyFeatures(it, resp, s)
		forms, orig := g.imageForms(c.Data, resp, s)
		maps.Copy(payloads, forms)
		original = orig
	case pb.ItemType_ITEM_TYPE_RECORD, pb.ItemType_ITEM_TYPE_DIGEST:
		// Small structured items: the record is its own FACT; no ladder.
		payloads[pb.FormKind_FORM_KIND_FULL] = c.Data
	default:
		payloads[pb.FormKind_FORM_KIND_FULL] = c.Data
	}
	payloads[pb.FormKind_FORM_KIND_FACT] = fact.FromItem(it).Marshal()
	if c.Type == pb.ItemType_ITEM_TYPE_RECORD || c.Type == pb.ItemType_ITEM_TYPE_DIGEST {
		// For records the payload is small and is the fact.
		payloads[pb.FormKind_FORM_KIND_FACT] = c.Data
		delete(payloads, pb.FormKind_FORM_KIND_FULL)
	}
	if len(only) > 0 {
		for k := range payloads {
			if !slices.Contains(only, k) {
				delete(payloads, k)
			}
		}
	}
	if _, err := g.st.Put(ctx, it, payloads); err != nil {
		return nil, err
	}
	if _, full := payloads[pb.FormKind_FORM_KIND_FULL]; original != nil && full {
		if err := g.st.PutOriginal(it.Id, original); err != nil {
			return nil, err
		}
	}
	row, err := g.st.Get(ctx, it.Id)
	if err != nil {
		return nil, err
	}
	if err := g.sch.ScoreItem(ctx, row); err != nil {
		return nil, err
	}
	g.Count++
	if g.OnItem != nil {
		g.OnItem(row)
	}
	return row, nil
}

// applyFeatures copies the engine's view of the frame into the item. For a
// sighting the item is about that object, so its key detection, key-crop
// embedding and unknownness take precedence over the per-frame values
// (which an engine reporting several objects in one frame cannot split).
func (g *Ingester) applyFeatures(it *pb.Item, resp *pb.AnalyzeResponse, s *pb.Sighting) {
	f := it.Features
	f.Detections, f.Embedding, f.Unknownness, f.ModelId = resp.Detections, resp.Embedding, resp.Unknownness, resp.ModelId
	if s == nil {
		return
	}
	if s.KeyDetection != nil {
		f.Detections = []*pb.Detection{s.KeyDetection}
	}
	if len(s.Embedding) > 0 {
		f.Embedding, f.Unknownness = s.Embedding, s.Unknownness
	} else if len(resp.Sightings) > 1 {
		f.Embedding = nil // the frame embedding belongs to one of several objects; do not guess
	}
	if f.Tags == nil {
		f.Tags = map[string]string{}
	}
	f.Tags["sighting_id"] = s.Id
	f.Tags["event"] = eventTag(s.Event)
	if s.Subtype != "" {
		f.Tags["subtype"] = s.Subtype
	}
}

// imageForms produces THUMB, FULL and CROP (see Config). original is the
// capture when FULL was re-encoded, else nil.
func (g *Ingester) imageForms(data []byte, resp *pb.AnalyzeResponse, s *pb.Sighting) (payloads map[pb.FormKind][]byte, original []byte) {
	payloads = map[pb.FormKind][]byte{pb.FormKind_FORM_KIND_FULL: data}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		g.log.Warn("decode", "err", err)
		return payloads, nil
	}
	if th, err := g.thumbForm(img); err == nil {
		payloads[pb.FormKind_FORM_KIND_THUMB] = th
	} else {
		g.log.Warn("thumb", "err", err)
	}
	full := g.fullForm(data, img)
	payloads[pb.FormKind_FORM_KIND_FULL] = full
	if !bytes.Equal(full, data) {
		original = data
	}
	if !g.cfg.CropUnknowns && g.unknown(resp.GetUnknownness()) {
		return payloads, original // no CROP: the whole frame goes as FULL, for training
	}
	var cr []byte
	if b := s.GetKeyDetection().GetBox(); b != nil {
		cr, err = g.crop(img, b)
	} else {
		cr, err = g.cropForm(img, resp.GetDetections())
	}
	if err != nil {
		g.log.Warn("crop", "err", err)
	} else if cr != nil && len(cr) < len(full) {
		// A CROP no smaller than FULL only delays FULL on the ladder.
		payloads[pb.FormKind_FORM_KIND_CROP] = cr
	}
	return payloads, original
}

// replacementCrop cuts a CROP from a replacement key frame for an item that
// already announced one. The form set is fixed once the manifest is out, and
// a CROP from the previous frame would be wrong, so the usual gates (unknown,
// smaller than FULL) don't apply; with no box at all the whole frame is used.
func (g *Ingester) replacementCrop(data []byte, resp *pb.AnalyzeResponse, s *pb.Sighting) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if b := s.GetKeyDetection().GetBox(); b != nil {
		return g.crop(img, b)
	}
	if cr, err := g.cropForm(img, resp.GetDetections()); err != nil || cr != nil {
		return cr, err
	}
	return encodeJPEG(img, g.cfg.CropQual)
}

// crop cuts the normalized box with CropMargin context about its centre, at
// least CropMinShort px per side; a window that would leave the frame slides
// inside it rather than shrinking. No resampling.
func (g *Ingester) crop(img image.Image, b *pb.Box) ([]byte, error) {
	r := img.Bounds()
	W, H := float64(r.Dx()), float64(r.Dy())
	cx, cy := (float64(b.X)+float64(b.W)/2)*W, (float64(b.Y)+float64(b.H)/2)*H
	w, h := math.Max(float64(b.W)*W*g.cfg.CropMargin, float64(g.cfg.CropMinShort)), math.Max(float64(b.H)*H*g.cfg.CropMargin, float64(g.cfg.CropMinShort))
	x0, x1 := window(cx, w, W)
	y0, y1 := window(cy, h, H)
	rect := image.Rect(x0, y0, x1, y1).Add(r.Min)
	if rect.Dx() < 1 || rect.Dy() < 1 {
		return nil, fmt.Errorf("crop: empty box %v", b)
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: g.cfg.CropQual}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// window places a span of size n centred on c inside [0, limit].
func window(c, n, limit float64) (int, int) {
	n = math.Min(n, limit)
	lo := math.Max(c-n/2, 0)
	lo = math.Min(lo, limit-n)
	return int(math.Round(lo)), int(math.Round(lo + n))
}

// entropyFor seeds ULID entropy from the edge id, so two edges capturing
// in the same millisecond never mint the same id, while a single edge stays
// deterministic for tests and simulations. Uniqueness within an edge comes
// from the monotonic counter + timestamp.
func entropyFor(edgeID string) io.Reader {
	h := fnv.New64a()
	_, _ = h.Write([]byte(edgeID))
	return rand.New(rand.NewSource(int64(h.Sum64())))
}
