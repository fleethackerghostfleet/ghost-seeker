package shorebridge

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func (c *Console) SetReviewLog(l *reviews.Log) { c.reviews = l }
func (c *Console) reviewAvailable(w http.ResponseWriter) bool {
	if c.reviews == nil {
		writeError(w, 503, "review logging disabled")
		return false
	}
	return true
}
func (c *Console) reviewItem(w http.ResponseWriter, r *http.Request) {
	if !c.reviewAvailable(w) {
		return
	}
	var b struct {
		Reviewer string `json:"reviewer"`
		Action   string `json:"action"`
		Tier     int    `json:"tier"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeReviewBody(r.Body, &b); err != nil {
		writeReviewBodyError(w, err)
		return
	}
	b.Reviewer = reviews.Trim(b.Reviewer, 80)
	if b.Reviewer == "" {
		writeError(w, 400, "reviewer required")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	base := c.base[r.PathValue("id")]
	if base == nil {
		http.NotFound(w, r)
		return
	}
	if isScene(base) {
		writeError(w, http.StatusConflict, "camera overviews cannot be reviewed or escalated")
		return
	}
	old := c.withOverlay(base)
	effect, err := reviews.ReviewEffect(b.Action, b.Tier, old.CentralTier)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	directives := []string{}
	if c.policy.Text != "" {
		directives = append(directives, c.policy.Text)
	}
	var provenance *reviews.LiveProvenance
	if l := old.Live; l != nil {
		form := l.ImageForm
		if form == "" {
			form = "fact"
		}
		provenance = &reviews.LiveProvenance{ShoreItemID: l.ItemID, SightingID: l.SightingID, Source: old.SourceID, Camera: c.sources[old.SourceID].Name, Label: l.Label, Subtype: l.Subtype, Unknownness: l.Unknownness, ImageForm: form}
	}
	rec, err := c.reviews.Append(reviews.Record{Action: b.Action, ItemID: old.ID, Img: old.Img, SourceID: old.SourceID, TS: old.TS, Reviewer: b.Reviewer, Model: c.live.Prioritization.Label, ModelKey: "live:" + c.live.Prioritization.Mode, ModelTier: old.CentralTier, ModelScore: old.Central, Edge: old.Edge, BaseTier: old.BaseTier, HumanTier: effect.HumanTier, Agreed: effect.HumanTier == old.CentralTier, Detections: old.Dets, Scores: old.Det, Directives: directives, Note: reviews.Trim(b.Note, 280), Before: old, Live: provenance})
	if err != nil {
		writeError(w, 500, "cannot persist review")
		return
	}
	c.publish("review-data", c.reviews.Summary())
	o := c.overlayFor(old.ID)
	o.Manual, o.Reviewed = effect.Tier, true
	if effect.Dismissed != nil {
		o.Dismissed = *effect.Dismissed
	}
	if effect.Sent != nil && !o.Sent {
		now := time.Now()
		o.Sent, o.SentAt, o.Decision, o.DecisionNote, o.DecidedAt = true, &now, "", "", nil
	}
	item, cdr := c.withOverlay(base), c.cdrLocked()
	c.publish("update", map[string]any{"item": item, "cdr": cdr})
	writeJSON(w, map[string]any{"item": item, "cdr": cdr, "record": rec})
}
func (c *Console) correctReview(w http.ResponseWriter, r *http.Request) {
	if !c.reviewAvailable(w) {
		return
	}
	var b struct {
		Reviewer string `json:"reviewer"`
		Reason   string `json:"reason"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeReviewBody(r.Body, &b); err != nil {
		writeReviewBodyError(w, err)
		return
	}
	b.Reviewer = reviews.Trim(b.Reviewer, 80)
	if b.Reviewer == "" {
		writeError(w, 400, "reviewer required")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	action := "reason"
	if strings.HasSuffix(r.URL.Path, "/undo") {
		action = "undo"
	}
	var original reviews.Record
	found := false
	effective := c.reviews.Effective()
	for _, v := range effective {
		if v.ID == r.PathValue("id") {
			original, found = v, true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if action == "reason" && b.Reason != "" && b.Reason != "wrong_detection" {
		valid := false
		for _, d := range original.Detections {
			if d.Class == b.Reason {
				valid = true
			}
		}
		if !valid {
			writeError(w, 400, "reason must name a detection class or wrong_detection")
			return
		}
	}
	if action == "undo" {
		for _, v := range effective {
			if v.ItemID == original.ItemID && v.ReviewedAt.After(original.ReviewedAt) {
				writeError(w, 409, "undo newer reviews first")
				return
			}
		}
	}
	rec, err := c.reviews.Correct(original.ID, b.Reviewer, action, b.Reason, reviews.Trim(b.Note, 280))
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	c.publish("review-data", c.reviews.Summary())
	if action == "undo" {
		before := original.Before
		o := c.overlayFor(original.ItemID)
		o.Manual, o.Reviewed, o.Dismissed, o.Sent, o.SentAt = before.Manual, before.Reviewed, before.Dismissed, before.Sent, before.SentAt
		o.Decision, o.DecisionNote, o.DecidedAt = before.Decision, before.DecisionNote, before.DecidedAt
		if base := c.base[original.ItemID]; base != nil {
			item, cdr := c.withOverlay(base), c.cdrLocked()
			c.publish("update", map[string]any{"item": item, "cdr": cdr})
			writeJSON(w, map[string]any{"record": rec, "item": item, "cdr": cdr})
			return
		}
	}
	writeJSON(w, map[string]any{"record": rec})
}
func (c *Console) reviewSummary(w http.ResponseWriter, r *http.Request) {
	if c.reviewAvailable(w) {
		writeJSON(w, c.reviews.Summary())
	}
}

func decodeReviewBody(reader io.Reader, dst any) error {
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing data")
	}
	return nil
}

func writeReviewBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, 413, "request body too large")
		return
	}
	writeError(w, 400, "invalid request body")
}
func (c *Console) reviewExport(w http.ResponseWriter, r *http.Request) {
	if !c.reviewAvailable(w) {
		return
	}
	f := r.URL.Query().Get("format")
	if f != "csv" && f != "jsonl" {
		writeError(w, 400, "format must be csv or jsonl")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=reviews."+f)
	if f == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/x-ndjson")
	}
	_ = c.reviews.Export(w, f)
}
