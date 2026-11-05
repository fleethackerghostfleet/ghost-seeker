package ingest

import (
	"bytes"
	"image"
	"sync"
	"sync/atomic"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// FrameEvent is what happened to one camera frame: it opened a new item,
// folded into an existing one (duplicate), replaced an item's key frame, or
// was dropped. The ship console shows these as the raw camera stream.
type FrameEvent struct {
	Seq      int64  `json:"seq"`
	AtUnixMs int64  `json:"atUnixMs"`
	Source   string `json:"source"`
	Outcome  string `json:"outcome"` // new | folded | updated | closed | dropped | error
	ItemID   string `json:"itemId,omitempty"`
	Label    string `json:"label,omitempty"`
	Bytes    int    `json:"bytes"`
	thumb    []byte
}

// FrameLog keeps the last few hundred frame outcomes (with small thumbnails)
// and per-item fold counts, in memory only.
type FrameLog struct {
	mu     sync.Mutex
	size   int
	seq    int64
	ring   []FrameEvent
	folds  map[string]int64
	counts map[string]int64
	raw    int64
}

func NewFrameLog(size int) *FrameLog {
	return &FrameLog{size: size, folds: map[string]int64{}, counts: map[string]int64{}}
}

func (l *FrameLog) add(e FrameEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq = l.seq
	l.ring = append(l.ring, e)
	if len(l.ring) > l.size {
		l.ring = l.ring[len(l.ring)-l.size:]
	}
	l.counts[e.Outcome]++
	l.raw += int64(e.Bytes)
	if e.Outcome == "folded" && e.ItemID != "" {
		l.folds[e.ItemID]++
	}
}

// Since returns up to limit of the newest events with Seq > after (oldest
// first), the per-outcome frame counts, and the raw camera bytes seen.
func (l *FrameLog) Since(after int64, limit int) ([]FrameEvent, map[string]int64, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []FrameEvent
	for _, e := range l.ring {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	counts := make(map[string]int64, len(l.counts))
	for k, v := range l.counts {
		counts[k] = v
	}
	return out, counts, l.raw
}

func (l *FrameLog) Thumb(seq int64) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.ring {
		if e.Seq == seq {
			return e.thumb
		}
	}
	return nil
}

// Folds is how many later frames were folded into item id as duplicates.
func (l *FrameLog) Folds(id string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.folds[id]
}

// logFrame records the outcome of one image capture.
func (g *Ingester) logFrame(c *Capture, row *store.Row, err error, dups, count int64) {
	e := FrameEvent{AtUnixMs: c.CapturedAt.UnixMilli(), Source: c.Source, Bytes: len(c.Data)}
	if c.CapturedAt.IsZero() {
		e.AtUnixMs = g.clk.Now().UnixMilli()
	}
	switch {
	case err != nil:
		e.Outcome = "error"
	case g.Count > count && row != nil && row.Item.Type == pb.ItemType_ITEM_TYPE_RECORD:
		e.Outcome = "closed"
	case g.Count > count:
		e.Outcome = "new"
	case atomic.LoadInt64(&g.Dups) > dups:
		e.Outcome = "folded"
	case row != nil:
		e.Outcome = "updated"
	default:
		e.Outcome = "dropped"
	}
	if row != nil {
		e.ItemID = row.Item.Id
		e.Label, _ = scoring.TopLabel(row.Item, 0)
		if row.Item.GetFeatures().GetTags()["event"] == "scene" {
			e.Label = "scene"
		}
	} else if e.Outcome == "folded" {
		for _, s := range g.lastResp.GetSightings() {
			if id, ok := g.sightings[s.Id]; ok {
				e.ItemID, e.Label = id, s.GetKeyDetection().GetLabel()
				break
			}
		}
		if e.ItemID == "" && len(g.lastResp.GetDetections()) == 0 {
			e.Label = "empty water"
		}
	}
	if img, _, derr := image.Decode(bytes.NewReader(c.Data)); derr == nil {
		e.thumb, _ = g.thumbForm(img)
	}
	g.Frames.add(e)
}
