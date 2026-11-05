package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/command"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/reviews"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/store"
)

func TestReviews(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	l, e := reviews.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	d := store.New(10)
	d.Add(&command.Item{ID: "i", Central: .75, CentralTier: 2, BaseTier: 3, DatasetID: "seaships7000", ImageID: "seaships7000/valid/1", SourceVideo: "SeaShips", SHA256: strings.Repeat("a", 64)}, 0)
	stored, _ := d.GetItem("i")
	s := New(d)
	s.SetReviewLog(l)
	peer, _ := s.hub.add()
	defer s.hub.remove(peer)
	assertSummary := func(count int) {
		t.Helper()
		for len(peer.events) > 0 {
			event := <-peer.events
			if event.name != "review-data" {
				continue
			}
			var summary struct {
				Count int `json:"count"`
			}
			data, _ := json.Marshal(event.data)
			if err := json.Unmarshal(data, &summary); err != nil || summary.Count != count {
				t.Fatalf("peer summary: %s, err=%v", event.data, err)
			}
			return
		}
		t.Fatal("no peer review summary event")
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	if w := post("/api/items/i/review", `{"reviewer":"  ","action":"accept"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := post("/api/items/i/review", `{"reviewer":" A ","action":"tier","tier":1}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertSummary(1)
	var v struct {
		Record reviews.Record `json:"record"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.Record.ModelKey != "maritime" || v.Record.ModelScore != stored.Central || v.Record.ModelTier != stored.CentralTier || v.Record.Reviewer != "A" ||
		v.Record.DatasetID != stored.DatasetID || v.Record.ImageID != stored.ImageID || v.Record.SourceVideo != stored.SourceVideo || v.Record.SHA256 != stored.SHA256 {
		t.Fatalf("%+v", v)
	}
	if w = post("/api/items/i/review", `{"reviewer":"A","action":"accept","model_score":0}`); w.Code != 400 {
		t.Fatal("accepted model injection")
	}
	l.Close()
	l, e = reviews.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	s.SetReviewLog(l)
	if l.Summary().Count != 1 {
		t.Fatal("persistence")
	}
	w = post("/api/reviews/"+v.Record.ID+"/undo", `{"reviewer":"A"}`)
	if w.Code != 200 || l.Summary().Count != 0 {
		t.Fatal(w.Body.String())
	}
	assertSummary(0)
	it, _ := d.GetItem("i")
	if it.Reviewed || it.Manual != 0 || it.HumanReviewed {
		t.Fatal("undo did not restore")
	}
}

func TestAutoSentReviewUndoRestoresHumanProvenance(t *testing.T) {
	for _, priorHuman := range []bool{false, true} {
		l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		d := store.New(10)
		d.Add(&command.Item{ID: "i", Manual: 1}, 0)
		d.AutoEscalate()
		if priorHuman {
			tier := 2
			d.PatchItem("i", &tier, nil, nil, nil)
		}
		s := New(d)
		s.SetReviewLog(l)
		post := func(path, body string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
			return w
		}
		w := post("/api/items/i/review", `{"reviewer":"A","action":"tier","tier":3}`)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result struct{ Record reviews.Record }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		w = post("/api/reviews/"+result.Record.ID+"/undo", `{"reviewer":"A"}`)
		item, _ := d.GetItem("i")
		if w.Code != 200 || item.HumanReviewed != priorHuman || !item.AutoSent || !item.Sent {
			t.Fatalf("undo priorHuman=%v: item=%+v response=%s", priorHuman, item, w.Body.String())
		}
	}
}

func TestAcceptRejectsChangedDisplayedPriority(t *testing.T) {
	l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	d := store.New(10)
	d.Add(&command.Item{ID: "active"}, 0)
	before, _ := d.GetItem("active")
	s := New(d)
	s.SetReviewLog(l)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/active/review", strings.NewReader(body)))
		return w
	}
	stale := before.CentralTier%4 + 1
	w := post(fmt.Sprintf(`{"reviewer":"A","action":"accept","tier":%d}`, stale))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "model priority changed") {
		t.Fatalf("stale accept: %d %s", w.Code, w.Body.String())
	}
	after, _ := d.GetItem("active")
	if after.Reviewed || after.Manual != before.Manual || l.Summary().Count != 0 {
		t.Fatalf("stale accept changed item or review log: %+v", after)
	}
	w = post(fmt.Sprintf(`{"reviewer":"A","action":"accept","tier":%d}`, before.CentralTier))
	if w.Code != 200 {
		t.Fatalf("refreshed accept: %d %s", w.Code, w.Body.String())
	}
	after, _ = d.GetItem("active")
	if !after.Reviewed || after.Manual != before.CentralTier || l.Summary().Count != 1 {
		t.Fatalf("refreshed accept was not saved: %+v", after)
	}
	if w = post(`{"reviewer":"A","action":"accept"}`); w.Code != 200 {
		t.Fatalf("legacy accept without tier: %d %s", w.Code, w.Body.String())
	}
}

func TestReviewPreservesEscalation(t *testing.T) {
	for _, action := range []string{"tier", "accept", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			l, err := reviews.Open(filepath.Join(t.TempDir(), "log"))
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			d := store.New(10)
			d.Add(&command.Item{ID: "i"}, 0)
			yes := true
			d.PatchItem("i", nil, nil, nil, &yes)
			s := New(d)
			s.SetReviewLog(l)
			w := httptest.NewRecorder()
			tier := 2
			if action == "accept" {
				item, _ := d.GetItem("i")
				tier = item.CentralTier
			}
			s.ServeHTTP(w, httptest.NewRequest("POST", "/api/items/i/review", strings.NewReader(fmt.Sprintf(`{"reviewer":"A","action":%q,"tier":%d}`, action, tier))))
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			item, _ := d.GetItem("i")
			if !item.Sent || !item.Reviewed {
				t.Fatalf("review lost escalation: %+v", item)
			}
		})
	}
}

