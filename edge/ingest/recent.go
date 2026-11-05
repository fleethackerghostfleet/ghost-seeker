package ingest

import (
	"time"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
)

// recentFrame is one buffered capture of a source: the frame bytes as they
// arrived and their content hash.
type recentFrame struct {
	at   time.Time
	data []byte
	hash string
	geo  *pb.Geo
	tags map[string]string
}

// remember keeps the frame in the source's ring so a sighting the engine
// reports late (a tentative single detection is opened tentative_after_s
// after it was seen) can still be cut from the frame that showed the object.
// The ring is bounded in age (FrameBufferAge) and bytes (FrameBufferBytes).
func (g *Ingester) remember(c *Capture, hash string) {
	if g.cfg.FrameBufferAge <= 0 || g.cfg.FrameBufferBytes <= 0 || len(c.Data) == 0 {
		return
	}
	if g.recent == nil {
		g.recent = map[string][]recentFrame{}
	}
	for src, ring := range g.recent { // a source that went quiet does not keep its frames
		if src != c.Source && len(ring) > 0 && c.CapturedAt.Sub(ring[len(ring)-1].at) > g.cfg.FrameBufferAge {
			delete(g.recent, src)
		}
	}
	ring := append(g.recent[c.Source], recentFrame{at: c.CapturedAt, data: append([]byte(nil), c.Data...), hash: hash,
		geo: c.Geo, tags: c.Tags})
	size := 0
	for _, f := range ring {
		size += len(f.data)
	}
	cut := 0
	for cut < len(ring)-1 && (c.CapturedAt.Sub(ring[cut].at) > g.cfg.FrameBufferAge || size > g.cfg.FrameBufferBytes) {
		size -= len(ring[cut].data)
		cut++
	}
	g.recent[c.Source] = ring[cut:]
}

// keyCapture returns the capture a sighting's image forms should be cut from.
// The engine reports the object's box in the frame it last saw it; when that
// is older than the frame in hand (a tentative OPEN after tentative_after_s,
// or any OPEN carrying an older last_seen), the buffered frame nearest
// last_seen is used instead, so the item shows the object rather than the
// water it left behind. Falls back to the frame in hand.
func (g *Ingester) keyCapture(c *Capture, hash string, s *pb.Sighting) (*Capture, string) {
	last := s.GetLastSeenUnixMs()
	if last <= 0 || c.CapturedAt.IsZero() || c.CapturedAt.UnixMilli() <= last {
		return c, hash
	}
	best, bestD := -1, c.CapturedAt.UnixMilli()-last
	for i, f := range g.recent[c.Source] {
		d := f.at.UnixMilli() - last
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return c, hash
	}
	f := g.recent[c.Source][best]
	if f.hash == hash {
		return c, hash
	}
	kc := *c
	kc.Data, kc.CapturedAt, kc.Geo, kc.Tags = f.data, f.at, f.geo, f.tags
	g.LateKeyFrames++
	return &kc, f.hash
}
