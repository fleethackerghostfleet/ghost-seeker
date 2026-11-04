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
	"image"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/image/draw"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
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
	ThumbWidth  int
	ThumbQual   int
	KnownLabels []string
}

func DefaultConfig() Config {
	return Config{ThumbWidth: 160, ThumbQual: 55,
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
	return &Ingester{cfg: cfg, st: st, an: an, sch: sch, clk: clk, log: log, ent: ulid.Monotonic(seededReader{}, 0)}
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
		if th, err := g.thumb(c.Data); err == nil {
			payloads[pb.FormKind_FORM_KIND_THUMB] = th
		} else {
			g.log.Warn("thumb", "err", err)
		}
		payloads[pb.FormKind_FORM_KIND_FULL] = c.Data
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

func (g *Ingester) thumb(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w := g.cfg.ThumbWidth
	h := b.Dy() * w / max(b.Dx(), 1)
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: g.cfg.ThumbQual}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// seededReader gives ULIDs deterministic entropy per process; uniqueness
// comes from the monotonic counter + timestamp.
type seededReader struct{}

func (seededReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i*7 + 13)
	}
	return len(p), nil
}
