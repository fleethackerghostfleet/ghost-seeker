package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestReviewSync(t *testing.T) {
	s := New(store.New(10))
	c, _ := s.hub.add()
	defer s.hub.remove(c)
	body := map[string]any{"cls": "bird", "dir": "raise", "n": 4, "of": 5, "from": 3, "to": 2, "reviewer": "Alice"}
	create := func() *httptest.ResponseRecorder { return request(s, "POST", "/api/review-sync/proposals", body) }
	r := create()
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var result struct {
		Proposal reviewProposal `json:"proposal"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id := result.Proposal.ID
	if result.Proposal.From != 3 || result.Proposal.To != 2 {
		t.Fatalf("tier change not kept: %+v", result.Proposal)
	}
	e := <-c.events
	if e.name != "review-sync" {
		t.Fatal(e.name)
	}
	if r = create(); r.Code != 200 || !strings.Contains(r.Body.String(), id) {
		t.Fatal(r.Code, r.Body.String())
	}
	select {
	case <-c.events:
		t.Fatal("dedupe published mutation")
	default:
	}
	base := "/api/review-sync/proposals/" + id
	r = request(s, "POST", base+"/vote", map[string]string{"reviewer": "Bob", "vote": "agree"})
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"Bob":"agree"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	voteEvent := <-c.events
	r = request(s, "POST", base+"/vote", map[string]string{"reviewer": "Bob", "vote": "disagree"})
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	<-c.events
	frozen, _ := json.Marshal(voteEvent.data)
	if !strings.Contains(string(frozen), `"Bob":"agree"`) {
		t.Fatal("event mutated", string(frozen))
	}
	r = request(s, "POST", base+"/resolve", map[string]string{"reviewer": "Alice", "outcome": "accepted"})
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"resolvedBy":"Alice"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	resolved, _ := json.Marshal((<-c.events).data)
	if !strings.Contains(string(resolved), `"status":"accepted"`) {
		t.Fatal(string(resolved))
	}
	for _, path := range []string{base + "/vote", base + "/resolve"} {
		r = request(s, "POST", path, map[string]string{"reviewer": "Bob", "vote": "agree", "outcome": "rejected"})
		if r.Code != 404 {
			t.Fatal(r.Code)
		}
	}
	r = request(s, "GET", "/api/review-sync", nil)
	if strings.TrimSpace(r.Body.String()) != `{"proposals":[]}` {
		t.Fatal(r.Body.String())
	}
}

func TestReviewSyncValidationAndCap(t *testing.T) {
	s := New(store.New(10))
	for _, body := range []map[string]any{
		{"cls": "nope", "dir": "raise", "n": 4, "of": 5, "reviewer": "A"},
		{"cls": "bird", "dir": "sideways", "n": 4, "of": 5, "reviewer": "A"},
		{"cls": "bird", "dir": "raise", "n": 4, "of": 5, "reviewer": " "},
		{"cls": "bird", "dir": "raise", "n": 4, "of": 5, "reviewer": strings.Repeat("界", 81)},
		{"cls": "bird", "dir": "raise", "n": 6, "of": 5, "reviewer": "A"},
		{"cls": "bird", "dir": "raise", "n": 4, "of": 5, "from": 5, "to": 2, "reviewer": "A"},
		{"cls": "bird", "dir": "raise", "n": 4, "of": 5, "from": 3, "to": -1, "reviewer": "A"},
	} {
		r := request(s, "POST", "/api/review-sync/proposals", body)
		if r.Code != 400 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/api/review-sync/proposals", "/api/review-sync/proposals/missing/vote", "/api/review-sync/proposals/missing/resolve"} {
		for _, body := range []string{`{`, `{"reviewer":"` + strings.Repeat("x", 17000) + `"}`} {
			r := httptest.NewRecorder()
			s.ServeHTTP(r, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			want := 400
			if len(body) > 16000 {
				want = 413
			}
			if r.Code != want {
				t.Fatal(path, r.Code, r.Body.String())
			}
		}
		r := request(s, "POST", path, map[string]string{"reviewer": "A", "vote": "bad", "outcome": "bad"})
		if r.Code != 400 {
			t.Fatal(r.Code)
		}
	}
	count := 0
	for cls := range catalog.Classes {
		for _, dir := range []string{"raise", "lower"} {
			r := request(s, "POST", "/api/review-sync/proposals", map[string]any{"cls": cls, "dir": dir, "n": 4, "of": 5, "reviewer": strings.Repeat("界", 80)})
			want := 201
			if count >= 20 {
				want = 429
			}
			if r.Code != want {
				t.Fatal(count, r.Code, r.Body.String())
			}
			count++
		}
	}
	if count <= 20 {
		t.Fatal("not enough catalog classes to exercise cap")
	}
}

func TestReviewSyncKeepsDistinctTierChanges(t *testing.T) {
	s := New(store.New(10))
	propose := func(from, to int) reviewProposal {
		r := request(s, "POST", "/api/review-sync/proposals", map[string]any{"cls": "bird", "dir": "lower", "n": 3, "of": 3, "from": from, "to": to, "reviewer": "A"})
		if r.Code != 201 && r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var result struct {
			Proposal reviewProposal `json:"proposal"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Proposal
	}
	a, b, again := propose(1, 2), propose(3, 4), propose(1, 2)
	if a.ID == b.ID || b.From != 3 || b.To != 4 {
		t.Fatalf("distinct tier change merged: %+v %+v", a, b)
	}
	if again.ID != a.ID {
		t.Fatalf("same tier change not deduplicated: %+v %+v", a, again)
	}
}
