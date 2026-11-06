package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
)

// DedupMode selects how captures turn into items.
type DedupMode int

const (
	// DedupHash folds exact SHA-256 duplicates into the existing item (default).
	DedupHash DedupMode = iota
	// DedupNaive creates one item per capture.
	DedupNaive
	// DedupSightings lets a stateful engine (edgeml sidecar) decide: one item
	// per object per pass, driven by AnalyzeResponse.sightings. Responses
	// without sightings fall back to DedupHash behaviour.
	DedupSightings
)

func ParseDedup(s string) (DedupMode, error) {
	switch strings.ToLower(s) {
	case "", "hash":
		return DedupHash, nil
	case "naive":
		return DedupNaive, nil
	case "sightings":
		return DedupSightings, nil
	}
	return 0, fmt.Errorf("dedup: unknown mode %q (naive|hash|sightings)", s)
}

func (m DedupMode) String() string {
	return [...]string{"hash", "naive", "sightings"}[m]
}

func eventTag(e pb.Sighting_Event) string {
	return strings.ToLower(strings.TrimPrefix(e.String(), "EVENT_"))
}

// replaceableForms lists the image forms a key-frame replacement rewrites.
var replaceableForms = []pb.FormKind{pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_CROP, pb.FormKind_FORM_KIND_FULL}

// applySightings runs the sighting state machine for one frame.
//
//	OPEN                      -> new item (tags sighting_id/subtype/event, CROP from key box)
//	UPDATE, is_key_frame      -> replace thumb/crop/full + features in place while no image
//	                             form of the item has started to ship; otherwise a new item
//	                             with the same sighting_id (shore groups by it)
//	UPDATE, !is_key_frame     -> rewrite tags/subtype and the FACT of the current item
//	DUPLICATE                 -> counted, no item
//	CLOSE                     -> tiny RECORD contact report, state dropped
//
// The invariant: shore never gets two full frames of one object unless the
// second is a materially better key frame (the engine decides "better").
// Every sighting of the frame is attempted; the first error is returned
// after the rest were applied, so one failing object does not drop the
// others' reports and the caller still sees the failure.
// Returns the first item touched this frame, or nil when nothing was stored.
func (g *Ingester) applySightings(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse) (*store.Row, error) {
	g.stateful[c.Source] = true
	var first *store.Row
	var firstErr error
	for _, s := range resp.Sightings {
		row, err := g.applySighting(ctx, c, hash, resp, s)
		if err != nil {
			g.log.Warn("sighting", "id", s.Id, "event", eventTag(s.Event), "err", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("sighting %s %s: %w", s.Id, eventTag(s.Event), err)
			}
			continue
		}
		if first == nil {
			first = row
		}
	}
	return first, firstErr
}

func (g *Ingester) applySighting(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting) (*store.Row, error) {
	switch s.Event {
	case pb.Sighting_EVENT_OPEN:
		kc, kh := g.keyCapture(c, hash, s)
		return g.openItem(ctx, kc, kh, resp, s)
	case pb.Sighting_EVENT_UPDATE:
		id, known := g.sightings[s.Id]
		if !s.IsKeyFrame {
			if !known {
				return nil, nil
			}
			return g.retag(ctx, id, s)
		}
		if known {
			if row, err := g.st.Get(ctx, id); err == nil && !imageShipped(row) {
				return g.replaceKeyFrame(ctx, row, c, hash, resp, s)
			}
		}
		return g.openItem(ctx, c, hash, resp, s)
	case pb.Sighting_EVENT_DUPLICATE:
		atomic.AddInt64(&g.Dups, 1)
		id, known := g.sightings[s.Id]
		if !known || s.GetKeyDetection().GetBox() == nil {
			return nil, nil
		}
		var dark *pb.Detection
		for _, detection := range resp.Detections {
			if strings.EqualFold(detection.GetLabel(), "dark contact") &&
				boxIoU(detection.GetBox(), s.KeyDetection.GetBox()) >= 0.5 {
				dark = detection
				break
			}
		}
		if dark == nil {
			return nil, nil
		}
		row, err := g.st.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if row.Item.Features == nil {
			row.Item.Features = &pb.Features{}
		}
		if row.Item.Features.Tags == nil {
			row.Item.Features.Tags = map[string]string{}
		}
		if _, tagged := row.Item.Features.Tags["dark_contact"]; tagged {
			existingIndex := -1
			var existingConfidence float32
			for i, detection := range row.Item.Features.Detections {
				if strings.EqualFold(detection.GetLabel(), "dark contact") &&
					(existingIndex < 0 || detection.GetConfidence() > existingConfidence) {
					existingIndex = i
					existingConfidence = detection.GetConfidence()
				}
			}
			if existingIndex >= 0 {
				if dark.GetConfidence() <= existingConfidence {
					return nil, nil
				}
				row.Item.Features.Detections[existingIndex] = dark
			} else {
				row.Item.Features.Detections = append(row.Item.Features.Detections, dark)
			}
		} else {
			row.Item.Features.Detections = append(row.Item.Features.Detections, dark)
			row.Item.Features.Tags["dark_contact"] = "1"
		}
		if err := g.st.UpdateItem(ctx, row.Item); err != nil {
			return nil, err
		}
		return g.rescore(ctx, id)
	case pb.Sighting_EVENT_CLOSE:
		id, known := g.sightings[s.Id]
		delete(g.sightings, s.Id)
		if !known {
			return nil, nil
		}
		return g.closeRecord(ctx, c, id, s)
	}
	return nil, nil
}

func (g *Ingester) openItem(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting) (*store.Row, error) {
	row, err := g.newItem(ctx, c, hash, resp, s)
	if err != nil {
		return nil, err
	}
	g.sightings[s.Id] = row.Item.Id
	return row, nil
}

// imageShipped reports whether any image form of the item has started to
// reach shore, after which the item is immutable and a better key frame
// becomes a new item.
func imageShipped(row *store.Row) bool {
	for _, k := range replaceableForms {
		if f := row.Form(k); f != nil && (f.AckedThrough > 0 || f.Complete) {
			return true
		}
	}
	return false
}

// replaceKeyFrame swaps the item's image payloads and features for a better
// key frame of the same object and re-scores it. Forms the item never had
// (e.g. CROP when the first key frame carried no box) are not added; a CROP
// it already has is always re-cut from the new frame.
func (g *Ingester) replaceKeyFrame(ctx context.Context, row *store.Row, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting) (*store.Row, error) {
	it := row.Item
	it.ContentHash = hash
	g.applyFeatures(it, resp, s)
	payloads, original := g.imageForms(c.Data, resp, s)
	if err := g.keepOriginal(ctx, it.Id, original); err != nil {
		return nil, err
	}
	if _, ok := payloads[pb.FormKind_FORM_KIND_CROP]; !ok && row.Form(pb.FormKind_FORM_KIND_CROP) != nil {
		cr, err := g.replacementCrop(c.Data, resp, s)
		if err != nil {
			return nil, fmt.Errorf("replace CROP: %w", err)
		}
		payloads[pb.FormKind_FORM_KIND_CROP] = cr
	}
	payloads[pb.FormKind_FORM_KIND_FACT] = fact.FromItem(it).Marshal()
	for k, data := range payloads {
		nf, err := g.replaceForm(ctx, it.Id, k, data)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("replace %s: %w", k, err)
		}
		setForm(it, nf)
	}
	if err := g.st.UpdateItem(ctx, it); err != nil {
		return nil, err
	}
	g.Replaced++
	return g.rescore(ctx, it.Id)
}

// retag records a metadata-only change (subtype flip) on the current item.
func (g *Ingester) retag(ctx context.Context, id string, s *pb.Sighting) (*store.Row, error) {
	row, err := g.st.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	it := row.Item
	if it.Features == nil {
		it.Features = &pb.Features{}
	}
	if it.Features.Tags == nil {
		it.Features.Tags = map[string]string{}
	}
	it.Features.Tags["event"] = eventTag(s.Event)
	delete(it.Features.Tags, "new_contact")
	if s.Subtype != "" {
		it.Features.Tags["subtype"] = s.Subtype
	}
	nf, err := g.replaceForm(ctx, it.Id, pb.FormKind_FORM_KIND_FACT, fact.FromItem(it).Marshal())
	if err != nil {
		return nil, err
	}
	setForm(it, nf)
	if err := g.st.UpdateItem(ctx, it); err != nil {
		return nil, err
	}
	return g.rescore(ctx, it.Id)
}

// setForm records a rewritten form's size/hash in the item's form list so
// the item view agrees with the stored payloads.
func setForm(it *pb.Item, nf *pb.Form) {
	for i, f := range it.Forms {
		if f.Kind == nf.Kind {
			it.Forms[i] = nf
			return
		}
	}
	it.Forms = append(it.Forms, nf)
	sort.Slice(it.Forms, func(i, j int) bool { return it.Forms[i].Kind < it.Forms[j].Kind })
}

func (g *Ingester) replaceForm(ctx context.Context, id string, kind pb.FormKind, data []byte) (*pb.Form, error) {
	for {
		form, err := g.st.ReplaceForm(ctx, id, kind, data)
		if !g.cfg.WaitForSpace || !errors.Is(err, store.ErrFull) {
			return form, err
		}
		if err := g.clk.Sleep(ctx, 5*time.Second); err != nil {
			return nil, err
		}
	}
}

// sceneItem handles a frame without sightings in sightings mode.
//
// Frames with detections: once the engine has reported a sighting for this
// source it is stateful, so a detected-but-unreported frame is the tracker
// warming up and folds (the OPEN follows with the key frame). Until then the
// engine may be stateless (inference.Mock, an older sidecar) and the frame
// keeps hash behaviour, so detected images are never lost.
func (g *Ingester) sceneEvery(c *Capture) time.Duration {
	if c.SceneEvery != nil {
		return *c.SceneEvery
	}
	return g.cfg.SceneEvery
}

// Object-free frames become a "scene" item at most once per SceneEvery per
// source; the rest fold (counted as deduped). A scene ships FACT+THUMB only;
// its FULL stays on the edge and is pull-only (see scheduler.PullOnly).
func (g *Ingester) sceneItem(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse) (*store.Row, error) {
	if len(resp.Detections) > 0 {
		if !g.stateful[c.Source] {
			// Hash behaviour, except that a scene item holding the same
			// bytes must not swallow a frame that now shows an object.
			ids, err := g.st.IDsByHash(ctx, hash)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				if row, err := g.st.Get(ctx, id); err == nil && row.Item.GetFeatures().GetTags()["event"] != "scene" {
					atomic.AddInt64(&g.Dups, 1)
					return row, nil
				}
			}
			return g.newItem(ctx, c, hash, resp, nil)
		}
		atomic.AddInt64(&g.Dups, 1)
		return nil, nil
	}
	ts := c.CapturedAt
	if ts.IsZero() {
		ts = g.clk.Now()
	}
	if last, seen := g.lastScene[c.Source]; seen && ts.Sub(last) < g.sceneEvery(c) {
		atomic.AddInt64(&g.Dups, 1)
		return nil, nil
	}
	tags := maps.Clone(c.Tags)
	if tags == nil {
		tags = map[string]string{}
	}
	tags["event"] = "scene"
	sc := *c
	sc.Tags = tags
	row, err := g.newItem(ctx, &sc, hash, resp, nil, pb.FormKind_FORM_KIND_FACT, pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_FULL)
	if err == nil {
		g.lastScene[c.Source] = ts
	}
	return row, err
}

func (g *Ingester) rescore(ctx context.Context, id string) (*store.Row, error) {
	row, err := g.st.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := g.sch.ScoreItem(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// ContactReport is the RECORD appended when a sighting closes.
type ContactReport struct {
	SightingID      string `json:"sighting_id"`
	ItemID          string `json:"item_id"`
	Label           string `json:"label,omitempty"`
	Subtype         string `json:"subtype,omitempty"`
	FirstSeenUnixMs int64  `json:"first_seen_unix_ms"`
	LastSeenUnixMs  int64  `json:"last_seen_unix_ms"`
	NFrames         int32  `json:"n_frames"`
}

func (g *Ingester) closeRecord(ctx context.Context, c *Capture, itemID string, s *pb.Sighting) (*store.Row, error) {
	rep := ContactReport{SightingID: s.Id, ItemID: itemID, Label: s.GetKeyDetection().GetLabel(), Subtype: s.Subtype,
		FirstSeenUnixMs: s.FirstSeenUnixMs, LastSeenUnixMs: s.LastSeenUnixMs, NFrames: s.NFrames}
	data, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	tags := map[string]string{"sighting_id": s.Id, "event": eventTag(s.Event)}
	if s.Subtype != "" {
		tags["subtype"] = s.Subtype
	}
	rc := &Capture{Type: pb.ItemType_ITEM_TYPE_RECORD, Data: data, CapturedAt: c.CapturedAt, Source: c.Source, Geo: c.Geo, Tags: tags}
	return g.newItem(ctx, rc, hex.EncodeToString(sum[:]), nil, nil)
}
