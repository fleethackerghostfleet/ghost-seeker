package server

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

type reviewLog interface {
	Append(reviews.Record) (reviews.Record, error)
	Effective() []reviews.Record
	Correct(string, string, string, string, string) (reviews.Record, error)
	Summary() reviews.Summary
	Export(io.Writer, string) error
}

func (s *Server) SetReviewLog(l *reviews.Log) {
	if l == nil {
		s.reviews = nil
		return
	}
	s.reviews = l
}
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
	var rec reviews.Record
	status := http.StatusInternalServerError
	item, cdr, err := s.store.CommitReview(r.PathValue("id"), func(old command.Item, state store.State) (store.ReviewPatch, error) {
		tier := b.Tier
		var dismissed, sent *bool
		yes := true
		switch b.Action {
		case "tier":
			if tier < 1 || tier > 4 {
				status = 400
				return store.ReviewPatch{}, errors.New("tier must be 1–4")
			}
		case "accept":
			if tier != 0 && tier != old.CentralTier {
				status = 409
				return store.ReviewPatch{}, errors.New("model priority changed — review the updated suggestion before accepting")
			}
			tier = old.CentralTier
		case "dismiss":
			tier = 4
			dismissed = &yes
		case "cdr":
			tier = 1
			sent = &yes
		default:
			status = 400
			return store.ReviewPatch{}, errors.New("unknown review action")
		}
		texts := []string{}
		for _, d := range state.Directives {
			texts = append(texts, d.Text)
		}
		human := tier
		if b.Action == "dismiss" {
			human = 0
		}
		reason := ""
		if old.DetectorNegative && human >= 1 && human <= 3 {
			reason = "missed_detection"
		}
		var err error
		rec, err = s.reviews.Append(reviews.Record{DetectorNegative: old.DetectorNegative, Reason: reason, Action: b.Action, ItemID: old.ID, Img: old.Img, SourceID: old.SourceID, DatasetID: old.DatasetID, ImageID: old.ImageID, SourceVideo: old.SourceVideo, SHA256: old.SHA256, TS: old.TS, Reviewer: b.Reviewer, Model: catalog.Models[state.Model].Name, ModelKey: state.Model, ModelTier: old.CentralTier, ModelScore: old.Central, Edge: old.Edge, BaseTier: old.BaseTier, HumanTier: human, Agreed: human == old.CentralTier, Detections: old.Dets, Scores: old.Det, Directives: texts, Note: reviews.Trim(b.Note, 280), Before: old})
		if err != nil {
			return store.ReviewPatch{}, errors.New("cannot persist review")
		}

		return store.ReviewPatch{Manual: &tier, Reviewed: &yes, Dismissed: dismissed, Sent: sent}, nil
	})
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	s.PublishUpdate(item, cdr)
	audit(r, "item.review", "id", item.ID, "record", rec.ID, "reviewer", b.Reviewer)
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
		valid := original.DetectorNegative && b.Reason == "missed_detection"
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
	audit(r, "review."+action, "record", original.ID, "reviewer", b.Reviewer)
	if action == "undo" {
		o := original.Before
		item, cdr, ok := s.store.PatchItem(o.ID, &o.Manual, &o.Reviewed, &o.Dismissed, &o.Sent)
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
