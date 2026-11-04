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
	_ "image/png"
	"io"
	"log/slog"
	"math/rand"
	"time"

	"github.com/oklog/ulid/v2"

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
	// CROP: JPEG of the union of detection boxes (confidence >= CropMinConf)
	// at native resolution, padded by CropPad × box size per side. Skipped
	// when there are no boxes or the crop exceeds CropMaxArea of the frame.
	// Unknowns (unknownness >= the policy's UnknownMin) get no CROP unless
	// CropUnknowns is set: their whole frame is kept for training, so the
	// ladder goes THUMB → FULL.
	CropQual     int
	CropPad      float64
	CropMinConf  float64
	CropMaxArea  float64
	CropUnknowns bool
	// FULL: JPEG/WebP captures are sent as-is; other formats are re-encoded
	// as JPEG at FullQual if smaller (<= 0 keeps the original bytes).
	FullQual    int
	KnownLabels []string
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
		CropQual: 80, CropPad: 0.15, CropMinConf: 0, CropMaxArea: 0.6,
		FullQual:    90,
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
	Dups   int64
}

func New(cfg Config, st *store.Store, an inference.Analyzer, sch *scheduler.Scheduler, clk clock.Clock, log *slog.Logger) *Ingester {
	if log == nil {
		log = slog.Default()
	}
	return &Ingester{cfg: cfg, st: st, an: an, sch: sch, clk: clk, log: log, ent: ulid.Monotonic(entropyFor(cfg.EdgeID), 0)}
}

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

// Ingest stores one capture: dedup by hash, analyze, build forms, score.
func (g *Ingester) Ingest(ctx context.Context, c *Capture) (*store.Row, error) {
	sum := sha256.Sum256(c.Data)
	hash := hex.EncodeToString(sum[:])
	if id, ok, err := g.st.HasHash(ctx, hash); err != nil {
		return nil, err
	} else if ok {
		g.Dups++
		return g.st.Get(ctx, id)
	}
	ts := c.CapturedAt
	if ts.IsZero() {
		ts = g.clk.Now()
	}
	it := &pb.Item{Id: ulid.MustNew(ulid.Timestamp(ts), g.ent).String(), Type: c.Type, CapturedAtUnixMs: ts.UnixMilli(),
		Source: c.Source, Geo: c.Geo, ContentHash: hash, Features: &pb.Features{Tags: c.Tags}}
	payloads := map[pb.FormKind][]byte{}
	switch c.Type {
	case pb.ItemType_ITEM_TYPE_IMAGE:
		resp, err := g.an.Analyze(ctx, c.Data, g.cfg.KnownLabels, c.Hints)
		if err != nil {
			return nil, fmt.Errorf("analyze: %w", err)
		}
		it.Features.Detections, it.Features.Embedding, it.Features.Unknownness, it.Features.ModelId =
			resp.Detections, resp.Embedding, resp.Unknownness, resp.ModelId
		img, _, err := image.Decode(bytes.NewReader(c.Data))
		if err != nil {
			g.log.Warn("decode", "err", err)
			payloads[pb.FormKind_FORM_KIND_FULL] = c.Data
			break
		}
		if th, err := g.thumbForm(img); err == nil {
			payloads[pb.FormKind_FORM_KIND_THUMB] = th
		} else {
			g.log.Warn("thumb", "err", err)
		}
		full := g.fullForm(c.Data, img)
		payloads[pb.FormKind_FORM_KIND_FULL] = full
		if !g.cfg.CropUnknowns && g.unknown(resp.Unknownness) {
			// No CROP: the whole frame goes as FULL, for training.
		} else if cr, err := g.cropForm(img, resp.Detections); err != nil {
			g.log.Warn("crop", "err", err)
		} else if cr != nil && len(cr) < len(full) {
			// A CROP no smaller than FULL only delays FULL on the ladder.
			payloads[pb.FormKind_FORM_KIND_CROP] = cr
		}
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
	if _, err := g.st.Put(ctx, it, payloads); err != nil {
		return nil, err
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

// entropyFor seeds ULID entropy from the edge id, so two edges capturing
// in the same millisecond never mint the same id, while a single edge stays
// deterministic for tests and simulations. Uniqueness within an edge comes
// from the monotonic counter + timestamp.
func entropyFor(edgeID string) io.Reader {
	h := fnv.New64a()
	_, _ = h.Write([]byte(edgeID))
	return rand.New(rand.NewSource(int64(h.Sum64())))
}
