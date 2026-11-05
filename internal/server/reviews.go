package server

import (
	"net/http"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
)

func (s *Server) SetReviewLog(l *reviews.Log) { s.reviews = l }
func (s *Server) reviewAvailable(w http.ResponseWriter) bool {
	if s.reviews == nil {
		writeError(w, 503, "review logging disabled")
		return false
	}
	return true
}
func (s *Server) reviewItem(w http.ResponseWriter, r *http.Request) {
	if !s.reviewAvailable(w) {
		return
	}
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	var b struct {
		Reviewer string `json:"reviewer"`
		Action   string `json:"action"`
		Tier     int    `json:"tier"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &b); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	b.Reviewer = reviews.Trim(b.Reviewer, 80)
	if b.Reviewer == "" {
		writeError(w, 400, "reviewer required")
		return
	}
	items, _, _, state := s.store.SnapshotWithDirectives()
	var old command.Item
	ok := false
	for _, item := range items {
		if item.ID == r.PathValue("id") {
			old, ok = item, true
			break
		}
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	effect, err := reviews.ReviewEffect(b.Action, b.Tier, old.CentralTier)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tier, dismissed, sent := effect.Tier, effect.Dismissed, effect.Sent
	yes := true
	texts := []string{}
	for _, d := range state.Directives {
		texts = append(texts, d.Text)
	}
	human := effect.HumanTier
	rec, err := s.reviews.Append(reviews.Record{Action: b.Action, ItemID: old.ID, Img: old.Img, SourceID: old.SourceID, TS: old.TS, Reviewer: b.Reviewer, Model: catalog.Models[state.Model].Name, ModelKey: state.Model, ModelTier: old.CentralTier, ModelScore: old.Central, Edge: old.Edge, BaseTier: old.BaseTier, HumanTier: human, Agreed: human == old.CentralTier, Detections: old.Dets, Scores: old.Det, Directives: texts, Note: reviews.Trim(b.Note, 280), Before: old})
	if err != nil {
		writeError(w, 500, "cannot persist review")
		return
	}
	s.hub.publish("review-data", s.reviews.Summary())
	item, cdr, ok := s.store.ApplyReview(old.ID, &tier, &yes, dismissed, sent, rec.ReviewedAt)
	if !ok {
		writeError(w, 409, "item evicted after review was logged")
		return
	}
	s.PublishUpdate(item, cdr)
	audit(r, "item.review", "id", old.ID, "record", rec.ID, "reviewer", b.Reviewer)
	writeJSON(w, 200, map[string]any{"item": item, "cdr": cdr, "record": rec})
}
func (s *Server) correctReview(w http.ResponseWriter, r *http.Request) {
	if !s.reviewAvailable(w) {
		return
	}
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	var b struct {
		Reviewer string `json:"reviewer"`
		Reason   string `json:"reason"`
		Note     string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := decodeJSON(r.Body, &b); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	b.Reviewer = reviews.Trim(b.Reviewer, 80)
	if b.Reviewer == "" {
		writeError(w, 400, "reviewer required")
		return
	}
	action := "reason"
	if strings.HasSuffix(r.URL.Path, "/undo") {
		action = "undo"
	}
	var original reviews.Record
	found := false
	for _, v := range s.reviews.Effective() {
		if v.ID == r.PathValue("id") {
			original = v
			found = true
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
		for _, v := range s.reviews.Effective() {
			if v.ItemID == original.ItemID && v.ReviewedAt.After(original.ReviewedAt) {
				writeError(w, 409, "undo newer reviews first")
				return
			}
		}
	}
	rec, err := s.reviews.Correct(original.ID, b.Reviewer, action, b.Reason, reviews.Trim(b.Note, 280))
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	s.hub.publish("review-data", s.reviews.Summary())
	audit(r, "review."+action, "record", original.ID, "reviewer", b.Reviewer)
	if action == "undo" {
		o := original.Before
		item, cdr, ok := s.store.RestoreReview(o, original.ReviewedAt)
		if ok {
			s.PublishUpdate(item, cdr)
			writeJSON(w, 200, map[string]any{"record": rec, "item": item, "cdr": cdr})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"record": rec})
}
func (s *Server) reviewSummary(w http.ResponseWriter, r *http.Request) {
	if s.reviewAvailable(w) {
		writeJSON(w, 200, s.reviews.Summary())
	}
}
func (s *Server) reviewExport(w http.ResponseWriter, r *http.Request) {
	if !s.reviewAvailable(w) {
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
	audit(r, "reviews.export", "format", f)
	_ = s.reviews.Export(w, f)
}
