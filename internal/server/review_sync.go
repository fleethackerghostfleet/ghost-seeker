package server

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/google/uuid"
)

type reviewProposal struct {
	ID         string            `json:"id"`
	Cls        string            `json:"cls"`
	Dir        string            `json:"dir"`
	N          int               `json:"n"`
	Of         int               `json:"of"`
	HighRisk   bool              `json:"highRisk"`
	From       int               `json:"from,omitempty"`
	To         int               `json:"to,omitempty"`
	ProposedBy string            `json:"proposedBy"`
	CreatedAt  time.Time         `json:"createdAt"`
	Votes      map[string]string `json:"votes"`
	Status     string            `json:"status"`
	ResolvedBy string            `json:"resolvedBy"`
}

func syncReviewer(w http.ResponseWriter, reviewer *string) bool {
	*reviewer = strings.TrimSpace(*reviewer)
	if *reviewer == "" || utf8.RuneCountInString(*reviewer) > 80 {
		writeError(w, 400, "reviewer must be 1–80 characters")
		return false
	}
	return true
}

func (s *Server) reviewSyncList(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	proposals := s.proposals
	if proposals == nil {
		proposals = []*reviewProposal{}
	}
	writeJSON(w, 200, map[string]any{"proposals": proposals})
}

func (s *Server) reviewSyncCreate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Cls      string `json:"cls"`
		Dir      string `json:"dir"`
		N        int    `json:"n"`
		Of       int    `json:"of"`
		HighRisk bool   `json:"highRisk"`
		From     int    `json:"from"`
		To       int    `json:"to"`
		Reviewer string `json:"reviewer"`
	}
	if err := decodeJSON(http.MaxBytesReader(w, r.Body, 16<<10), &b); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if !syncReviewer(w, &b.Reviewer) {
		return
	}
	if _, ok := catalog.Classes[b.Cls]; !ok {
		writeError(w, 400, "unknown class")
		return
	}
	if (b.Dir != "raise" && b.Dir != "lower") || b.N < 1 || b.Of < b.N {
		writeError(w, 400, "invalid direction or counts")
		return
	}
	if b.From < 0 || b.From > 4 || b.To < 0 || b.To > 4 {
		writeError(w, 400, "priority tiers must be 1-4")
		return
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	for _, p := range s.proposals {
		if p.Cls == b.Cls && p.Dir == b.Dir {
			writeJSON(w, 200, map[string]any{"proposal": p})
			return
		}
	}
	if len(s.proposals) >= 20 {
		writeError(w, 429, "too many pending proposals")
		return
	}
	p := &reviewProposal{ID: uuid.NewString(), Cls: b.Cls, Dir: b.Dir, N: b.N, Of: b.Of, HighRisk: b.HighRisk, From: b.From, To: b.To, ProposedBy: b.Reviewer, CreatedAt: time.Now().UTC(), Votes: map[string]string{}, Status: "pending"}
	s.proposals = append(s.proposals, p)
	s.publishReviewProposal(p)
	writeJSON(w, 201, map[string]any{"proposal": p})
}

func (s *Server) publishReviewProposal(p *reviewProposal) {
	copy := *p
	copy.Votes = make(map[string]string, len(p.Votes))
	for name, vote := range p.Votes {
		copy.Votes[name] = vote
	}
	s.hub.publish("review-sync", map[string]any{"proposal": copy})
}

func (s *Server) reviewSyncVote(w http.ResponseWriter, r *http.Request) {
	s.reviewSyncMutate(w, r, false)
}
func (s *Server) reviewSyncResolve(w http.ResponseWriter, r *http.Request) {
	s.reviewSyncMutate(w, r, true)
}

func (s *Server) reviewSyncMutate(w http.ResponseWriter, r *http.Request, resolve bool) {
	var b struct {
		Reviewer string `json:"reviewer"`
		Vote     string `json:"vote"`
		Outcome  string `json:"outcome"`
	}
	if err := decodeJSON(http.MaxBytesReader(w, r.Body, 16<<10), &b); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if !syncReviewer(w, &b.Reviewer) {
		return
	}
	if resolve && (b.Outcome != "accepted" && b.Outcome != "rejected") || !resolve && (b.Vote != "agree" && b.Vote != "disagree") {
		writeError(w, 400, "invalid vote or outcome")
		return
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	for i, p := range s.proposals {
		if p.ID != r.PathValue("id") {
			continue
		}
		if resolve {
			p.Status = b.Outcome
			p.ResolvedBy = b.Reviewer
			s.proposals = append(s.proposals[:i], s.proposals[i+1:]...)
		} else {
			p.Votes[b.Reviewer] = b.Vote
		}
		s.publishReviewProposal(p)
		writeJSON(w, 200, map[string]any{"proposal": p})
		return
	}
	writeError(w, 404, "pending proposal not found")
}
