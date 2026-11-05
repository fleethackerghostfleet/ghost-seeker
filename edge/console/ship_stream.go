package console

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
)

type shipForm struct {
	Kind     string `json:"kind"`
	Bytes    int64  `json:"bytes"`
	Acked    int64  `json:"acked"`
	Complete bool   `json:"complete"`
}

type shipItem struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	Label         string     `json:"label"`
	Override      string     `json:"override,omitempty"`
	Source        string     `json:"source"`
	CapturedAt    int64      `json:"capturedAtUnixMs"`
	Priority      float64    `json:"priority"`
	Suppressed    bool       `json:"suppressed"`
	Unknownness   float32    `json:"unknownness"`
	Event         string     `json:"event,omitempty"`
	Sighting      string     `json:"sightingId,omitempty"`
	Folded        int64      `json:"folded"`
	Pulled        bool       `json:"pulled"`
	ManifestAcked bool       `json:"manifestAcked"`
	Forms         []shipForm `json:"forms"`
	Reasons       []string   `json:"reasons,omitempty"`
}

func shipJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// ShipItemsHandler serves GET /ship/items: every item the edge holds, highest
// priority first, with what has reached shore per form and how many frames
// were folded into it as duplicates.
func (s *Service) ShipItemsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.St.All(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]shipItem, 0, len(rows))
		for _, row := range rows {
			it := row.Item
			label, _ := scoring.TopLabel(it, 0)
			tags := it.GetFeatures().GetTags()
			si := shipItem{ID: it.Id, Type: strings.TrimPrefix(it.Type.String(), "ITEM_TYPE_"), Label: label,
				Override: it.GetFeatures().GetLabelOverride(), Source: it.Source, CapturedAt: it.CapturedAtUnixMs,
				Priority: row.Score.GetPriority(), Suppressed: row.Score.GetSuppressed(), Unknownness: it.GetFeatures().GetUnknownness(),
				Event: tags["event"], Sighting: tags["sighting_id"], Pulled: row.PullBoost > 0, ManifestAcked: row.ManifestAcked}
			if s.Frames != nil {
				si.Folded = s.Frames.Folds(it.Id)
			}
			for _, f := range row.Forms {
				si.Forms = append(si.Forms, shipForm{Kind: strings.TrimPrefix(f.Kind.String(), "FORM_KIND_"), Bytes: f.Bytes, Acked: f.AckedThrough, Complete: f.Complete})
			}
			for _, rs := range row.Score.GetReasons() {
				if rs.Note != "" {
					si.Reasons = append(si.Reasons, rs.Scorer+": "+rs.Note)
				}
			}
			out = append(out, si)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
		shipJSON(w, map[string]any{"items": out})
	})
}

// ShipFramesHandler serves GET /ship/frames?after=SEQ (recent camera frames
// and what happened to each) and GET /ship/frames/{seq} (its thumbnail).
func (s *Service) ShipFramesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Frames == nil {
			shipJSON(w, map[string]any{"frames": []any{}})
			return
		}
		if v := r.PathValue("seq"); v != "" {
			seq, err := strconv.ParseInt(v, 10, 64)
			b := s.Frames.Thumb(seq)
			if err != nil || b == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "private, max-age=3600")
			_, _ = w.Write(b)
			return
		}
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		frames, counts, raw := s.Frames.Since(after, 120)
		shipJSON(w, map[string]any{"frames": frames, "counts": counts, "rawBytes": raw})
	})
}
