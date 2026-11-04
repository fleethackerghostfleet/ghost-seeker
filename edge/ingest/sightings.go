package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

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

// imageForms lists the forms a key-frame replacement rewrites.
var imageForms = []pb.FormKind{pb.FormKind_FORM_KIND_THUMB, pb.FormKind_FORM_KIND_CROP, pb.FormKind_FORM_KIND_FULL}

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
// Returns the first item touched this frame, or nil when nothing was stored.
func (g *Ingester) applySightings(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse) (*store.Row, error) {
	var first *store.Row
	for _, s := range resp.Sightings {
		row, err := g.applySighting(ctx, c, hash, resp, s)
		if err != nil {
			g.log.Warn("sighting", "id", s.Id, "event", eventTag(s.Event), "err", err)
			continue
		}
		if first == nil {
			first = row
		}
	}
	return first, nil
}

func (g *Ingester) applySighting(ctx context.Context, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting) (*store.Row, error) {
	switch s.Event {
	case pb.Sighting_EVENT_OPEN:
		return g.openItem(ctx, c, hash, resp, s)
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
		return nil, nil
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
	for _, k := range imageForms {
		if f := row.Form(k); f != nil && (f.AckedThrough > 0 || f.Complete) {
			return true
		}
	}
	return false
}

// replaceKeyFrame swaps the item's image payloads and features for a better
// key frame of the same object and re-scores it. Forms the item never had
// (e.g. CROP when the first key frame carried no box) are not added.
func (g *Ingester) replaceKeyFrame(ctx context.Context, row *store.Row, c *Capture, hash string, resp *pb.AnalyzeResponse, s *pb.Sighting) (*store.Row, error) {
	it := row.Item
	it.ContentHash = hash
	g.applyFeatures(it, resp, s)
	payloads := g.imageForms(c.Data, s)
	payloads[pb.FormKind_FORM_KIND_FACT] = fact.FromItem(it).Marshal()
	for k, data := range payloads {
		if _, err := g.st.ReplaceForm(ctx, it.Id, k, data); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("replace %s: %w", k, err)
		}
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
	if s.Subtype != "" {
		it.Features.Tags["subtype"] = s.Subtype
	}
	if _, err := g.st.ReplaceForm(ctx, it.Id, pb.FormKind_FORM_KIND_FACT, fact.FromItem(it).Marshal()); err != nil {
		return nil, err
	}
	if err := g.st.UpdateItem(ctx, it); err != nil {
		return nil, err
	}
	return g.rescore(ctx, it.Id)
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
